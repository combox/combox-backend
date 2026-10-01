package privacy

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Repository is the persistence of the privacy_settings table. A missing row
// means "the user never touched this parameter".
type Repository interface {
	// GetAll returns every stored row of one user.
	GetAll(ctx context.Context, userID string) ([]Row, error)
	// Upsert writes one (user, param) row.
	Upsert(ctx context.Context, userID, param, rule string, allowIDs, denyIDs []string) error
	// AreDirectContacts reports whether the two users share a direct chat.
	// The codebase has no dedicated contact list, so "my contacts" falls back
	// to "we have a 1-on-1 chat with each other".
	AreDirectContacts(ctx context.Context, userAID, userBID string) (bool, error)
}

// Service serves the settings API and is the single enforcement entry point
// (Evaluate) used by profile reads, avatar history and message serialisation.
type Service struct {
	repo Repository
}

// New builds the service.
func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("privacy repository is required")
	}
	return &Service{repo: repo}, nil
}

type memoKey struct{}

// memo carries one call chain's privacy reads so a page of messages costs at
// most one repository read per distinct target user, not one per message.
type memo struct {
	mu        sync.Mutex
	rows      map[string]map[string]Row
	decisions map[string]bool
	contacts  map[string]bool
}

func newMemo() *memo {
	return &memo{
		rows:      map[string]map[string]Row{},
		decisions: map[string]bool{},
		contacts:  map[string]bool{},
	}
}

// WithMemo returns a context carrying a fresh memo. Every public entry point
// that evaluates more than one parameter (message serialisation, profile
// reads, presence) wraps its context once and passes it down, so repository
// reads are deduplicated inside that batch.
func WithMemo(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, memoKey{}, newMemo())
}

// WithMemo mirrors the package function so the service satisfies the narrow
// interfaces its consumers (chat serialisation, handlers) declare.
func (s *Service) WithMemo(ctx context.Context) context.Context {
	return WithMemo(ctx)
}

func memoFrom(ctx context.Context) *memo {
	if ctx == nil {
		return nil
	}
	m, _ := ctx.Value(memoKey{}).(*memo)
	return m
}

func validUUID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func normalizeIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if !validUUID(id) {
			return nil, invalidArg("error.request.invalid_input", ErrInvalidID, map[string]string{"id": raw})
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func newSetting(row Row) Setting {
	allow := row.AllowIDs
	if allow == nil {
		allow = []string{}
	}
	deny := row.DenyIDs
	if deny == nil {
		deny = []string{}
	}
	return Setting{
		Param:      row.Param,
		Rule:       row.Rule,
		AllowIDs:   allow,
		DenyIDs:    deny,
		AllowCount: len(allow),
		DenyCount:  len(deny),
	}
}

func rowFor(rows map[string]Row, param string) (Row, bool) {
	if row, ok := rows[param]; ok {
		return row, true
	}
	return Row{Param: param, Rule: DefaultRule(param), AllowIDs: []string{}, DenyIDs: []string{}}, false
}

// loadRows reads the stored rows of one user, memoising the result inside the
// call chain's memo when one is attached to ctx.
func (s *Service) loadRows(ctx context.Context, m *memo, userID string) (map[string]Row, error) {
	if m != nil {
		m.mu.Lock()
		if rows, ok := m.rows[userID]; ok {
			m.mu.Unlock()
			return rows, nil
		}
		m.mu.Unlock()
	}
	raw, err := s.repo.GetAll(ctx, userID)
	if err != nil {
		return nil, internalErr(err)
	}
	rows := make(map[string]Row, len(raw))
	for _, row := range raw {
		if row.Param == "" {
			continue
		}
		rows[row.Param] = row
	}
	if m != nil {
		m.mu.Lock()
		m.rows[userID] = rows
		m.mu.Unlock()
	}
	return rows, nil
}

// Get returns every parameter with its rule, exception lists and counts, plus
// the default rule map. Parameters without a stored row come back with the
// default rule and empty lists; no rows are written for defaults.
func (s *Service) Get(ctx context.Context, userID string) (Result, error) {
	userID = strings.TrimSpace(userID)
	if !validUUID(userID) {
		return Result{}, invalidArg("error.request.invalid_input", ErrInvalidID, map[string]string{"user_id": userID})
	}
	rows, err := s.loadRows(ctx, memoFrom(ctx), userID)
	if err != nil {
		return Result{}, err
	}
	settings := make([]Setting, 0, len(Params))
	for _, param := range Params {
		row, ok := rowFor(rows, param)
		if !ok {
			row.Param = param
		}
		settings = append(settings, newSetting(row))
	}
	defaults := make(map[string]string, len(Defaults))
	for param, rule := range Defaults {
		defaults[param] = rule
	}
	return Result{Settings: settings, Defaults: defaults}, nil
}

// Set validates and stores one parameter. Unknown params, unknown rules and
// non-UUID ids are rejected with a typed invalid_argument error.
func (s *Service) Set(ctx context.Context, userID, param, rule string, allowIDs, denyIDs []string) (Setting, error) {
	userID = strings.TrimSpace(userID)
	param = strings.TrimSpace(param)
	rule = strings.TrimSpace(rule)
	if !validUUID(userID) {
		return Setting{}, invalidArg("error.request.invalid_input", ErrInvalidID, map[string]string{"user_id": userID})
	}
	if !IsParam(param) {
		return Setting{}, invalidArg("error.request.invalid_input", ErrUnknownParam, map[string]string{"param": param})
	}
	if !IsRule(rule) {
		return Setting{}, invalidArg("error.request.invalid_input", ErrInvalidRule, map[string]string{"rule": rule})
	}
	allow, err := normalizeIDs(allowIDs)
	if err != nil {
		return Setting{}, err
	}
	deny, err := normalizeIDs(denyIDs)
	if err != nil {
		return Setting{}, err
	}
	if err := s.repo.Upsert(ctx, userID, param, rule, allow, deny); err != nil {
		return Setting{}, internalErr(err)
	}
	return newSetting(Row{Param: param, Rule: rule, AllowIDs: allow, DenyIDs: deny}), nil
}

// Evaluate is the single enforcement entry point.
//
//	viewer == target (the owner) -> always allowed
//	viewer in deny_ids           -> denied (honoured by every rule)
//	rule everybody               -> allowed
//	rule nobody                  -> allowed only when viewer in allow_ids
//	rule contacts                -> allowed when viewer in allow_ids, or when
//	                                viewer shares a direct chat with target
//	                                ("my contacts" fallback: there is no
//	                                dedicated contact list in this codebase)
//	unknown rule stored in a row -> denied (fail closed)
//
// Reads are memoised per call chain when ctx carries WithMemo.
func (s *Service) Evaluate(ctx context.Context, viewerID, targetID, param string) (bool, error) {
	viewerID = strings.TrimSpace(viewerID)
	targetID = strings.TrimSpace(targetID)
	param = strings.TrimSpace(param)
	if !IsParam(param) {
		return false, invalidArg("error.request.invalid_input", ErrUnknownParam, map[string]string{"param": param})
	}
	if viewerID == "" || targetID == "" {
		return false, nil
	}
	if viewerID == targetID {
		return true, nil
	}

	m := memoFrom(ctx)
	if m != nil {
		key := viewerID + "|" + targetID + "|" + param
		m.mu.Lock()
		allowed, ok := m.decisions[key]
		m.mu.Unlock()
		if ok {
			return allowed, nil
		}
		decision, err := s.decide(ctx, m, viewerID, targetID, param)
		if err != nil {
			return false, err
		}
		m.mu.Lock()
		m.decisions[key] = decision
		m.mu.Unlock()
		return decision, nil
	}
	return s.decide(ctx, m, viewerID, targetID, param)
}

func (s *Service) decide(ctx context.Context, m *memo, viewerID, targetID, param string) (bool, error) {
	rows, err := s.loadRows(ctx, m, targetID)
	if err != nil {
		return false, err
	}
	row, ok := rowFor(rows, param)
	if !ok {
		row.Param = param
	}
	return s.evaluateRule(ctx, m, viewerID, targetID, row)
}

func (s *Service) evaluateRule(ctx context.Context, m *memo, viewerID, targetID string, row Row) (bool, error) {
	if containsID(row.DenyIDs, viewerID) {
		return false, nil
	}
	switch row.Rule {
	case RuleEverybody:
		return true, nil
	case RuleNobody:
		return containsID(row.AllowIDs, viewerID), nil
	case RuleContacts:
		if containsID(row.AllowIDs, viewerID) {
			return true, nil
		}
		return s.areContacts(ctx, m, viewerID, targetID)
	default:
		return false, nil
	}
}

func (s *Service) areContacts(ctx context.Context, m *memo, viewerID, targetID string) (bool, error) {
	key := viewerID + "|" + targetID
	if m != nil {
		m.mu.Lock()
		if ok, exists := m.contacts[key]; exists {
			m.mu.Unlock()
			return ok, nil
		}
		m.mu.Unlock()
	}
	ok, err := s.repo.AreDirectContacts(ctx, viewerID, targetID)
	if err != nil {
		return false, internalErr(err)
	}
	if m != nil {
		m.mu.Lock()
		m.contacts[key] = ok
		m.mu.Unlock()
	}
	return ok, nil
}

func containsID(ids []string, candidate string) bool {
	if candidate == "" {
		return false
	}
	for _, id := range ids {
		if id == candidate {
			return true
		}
	}
	return false
}
