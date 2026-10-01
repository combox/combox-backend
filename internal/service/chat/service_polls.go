package chat

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Poll request bounds (mirrored by the CHECK constraints in migration
// 000037_polls and by POLL_MAX_OPTIONS in combox-api).
const (
	pollMinOptions   = 2
	pollMaxQuestion  = 300
	pollMaxOptionLen = 100
	pollMaxAnswerLen = 600
)

// SetPollRepository wires the poll store. Every poll endpoint fails with an
// internal error when the repository was never configured.
func (s *Service) SetPollRepository(repo PollRepository) {
	s.polls = repo
}

func (s *Service) requirePolls() error {
	if s.polls == nil {
		return internal(errors.New("poll repository not configured"))
	}
	return nil
}

// CreatePoll stores a poll attached to a brand new chat message whose content
// is the question, so the message keeps every permission, notification and
// preview path a normal message has. Secret chats reject it through the
// existing plaintext guard inside CreateMessage.
func (s *Service) CreatePoll(ctx context.Context, input CreatePollInput) (Message, error) {
	userID := strings.TrimSpace(input.UserID)
	chatID := strings.TrimSpace(input.ChatID)
	question := strings.TrimSpace(input.Question)
	if userID == "" || chatID == "" || question == "" {
		return Message{}, invalidArg("error.poll.invalid_input")
	}
	if len([]rune(question)) > pollMaxQuestion {
		return Message{}, invalidArg("error.poll.invalid_input")
	}
	if err := s.requirePolls(); err != nil {
		return Message{}, err
	}

	options, err := normalizePollOptions(input.Options)
	if err != nil {
		return Message{}, err
	}

	description := strings.TrimSpace(input.Description)
	explanation := strings.TrimSpace(input.Explanation)
	for _, text := range []string{description, explanation} {
		if len([]rune(text)) > pollMaxAnswerLen {
			return Message{}, invalidArg("error.poll.invalid_input")
		}
	}

	correctIDs, err := resolveCorrectOptionIDs(options, input.CorrectOptionIDs, input.Multiple)
	if err != nil {
		return Message{}, err
	}

	var closesAt *time.Time
	if input.ClosesAt != nil {
		value := input.ClosesAt.UTC()
		if !value.After(s.nowUTC()) {
			return Message{}, invalidArg("error.poll.invalid_input")
		}
		closesAt = &value
	}

	message, err := s.CreateMessage(ctx, CreateMessageInput{
		UserID:  userID,
		ChatID:  chatID,
		Content: question,
	})
	if err != nil {
		return Message{}, err
	}

	poll, err := s.polls.CreatePoll(ctx, Poll{
		ChatID:           chatID,
		MessageID:        message.ID,
		Question:         question,
		Description:      optionalText(description),
		Options:          options,
		ShowWhoVoted:     input.ShowWhoVoted,
		Multiple:         input.Multiple,
		AllowAddOptions:  input.AllowAddOptions,
		AllowRevoting:    input.AllowRevoting,
		ShuffleOptions:   input.ShuffleOptions,
		CorrectOptionIDs: correctIDs,
		Explanation:      optionalText(explanation),
		ClosesAt:         closesAt,
		HideResults:      input.HideResults,
		IsClosed:         false,
		CreatedBy:        userID,
		CreatedAt:        s.nowUTC(),
	})
	if err != nil {
		return Message{}, internal(err)
	}

	view := s.buildPollView(poll, nil, userID)
	message.Poll = &view
	return message, nil
}

// GetPoll returns one poll for a viewer that can read its chat.
func (s *Service) GetPoll(ctx context.Context, userID, pollID string) (Poll, error) {
	userID = strings.TrimSpace(userID)
	pollID = strings.TrimSpace(pollID)
	if userID == "" || pollID == "" {
		return Poll{}, invalidArg("error.poll.invalid_input")
	}
	if err := s.requirePolls(); err != nil {
		return Poll{}, err
	}
	poll, err := s.polls.GetPoll(ctx, pollID)
	if err != nil {
		return Poll{}, mapChatOrMessageRepoError(err)
	}
	if err := s.ensurePollReadable(ctx, poll, userID); err != nil {
		return Poll{}, err
	}
	votes, err := s.pollVotes(ctx, poll.ID)
	if err != nil {
		return Poll{}, err
	}
	return s.buildPollView(poll, votes, userID), nil
}

// VotePoll casts (or replaces) the caller's ballot.
func (s *Service) VotePoll(ctx context.Context, input VotePollInput) (Poll, error) {
	userID := strings.TrimSpace(input.UserID)
	pollID := strings.TrimSpace(input.PollID)
	if userID == "" || pollID == "" {
		return Poll{}, invalidArg("error.poll.invalid_input")
	}
	if err := s.requirePolls(); err != nil {
		return Poll{}, err
	}
	poll, err := s.polls.GetPoll(ctx, pollID)
	if err != nil {
		return Poll{}, mapChatOrMessageRepoError(err)
	}
	if err := s.ensurePollReadable(ctx, poll, userID); err != nil {
		return Poll{}, err
	}
	if pollEffectivelyClosed(poll, s.nowUTC()) {
		return Poll{}, conflict("error.poll.closed")
	}

	optionIDs, err := validatePollAnswer(poll, input.OptionIDs)
	if err != nil {
		return Poll{}, err
	}

	votes, err := s.pollVotes(ctx, poll.ID)
	if err != nil {
		return Poll{}, err
	}
	previous := pollVoteByUser(votes, userID)
	if previous != nil && !poll.AllowRevoting {
		return Poll{}, conflict("error.poll.already_voted")
	}
	if err := s.polls.UpsertVote(ctx, poll.ID, userID, optionIDs, s.nowUTC()); err != nil {
		return Poll{}, internal(err)
	}

	// Reflect the new ballot locally instead of re-reading every ballot.
	if previous == nil {
		votes = append(votes, PollVote{UserID: userID, OptionIDs: optionIDs, VotedAt: s.nowUTC()})
	} else {
		previous.OptionIDs = optionIDs
		previous.VotedAt = s.nowUTC()
	}
	return s.buildPollView(poll, votes, userID), nil
}

// ClosePoll stops a poll early. The creator and the chat's owner/admins may
// do it; closing an already closed poll is a no-op.
func (s *Service) ClosePoll(ctx context.Context, userID, pollID string) (Poll, error) {
	userID = strings.TrimSpace(userID)
	pollID = strings.TrimSpace(pollID)
	if userID == "" || pollID == "" {
		return Poll{}, invalidArg("error.poll.invalid_input")
	}
	if err := s.requirePolls(); err != nil {
		return Poll{}, err
	}
	poll, err := s.polls.GetPoll(ctx, pollID)
	if err != nil {
		return Poll{}, mapChatOrMessageRepoError(err)
	}
	if err := s.ensurePollReadable(ctx, poll, userID); err != nil {
		return Poll{}, err
	}
	if !strings.EqualFold(poll.CreatedBy, userID) {
		role, roleErr := s.chats.GetChatMemberRole(ctx, poll.ChatID, userID)
		if roleErr != nil && !errors.Is(roleErr, ErrChatNotFound) {
			return Poll{}, internal(roleErr)
		}
		if !canEditChatByRole(role) {
			return Poll{}, forbidden("error.chat.forbidden")
		}
	}
	if !poll.IsClosed {
		if err := s.polls.ClosePoll(ctx, poll.ID); err != nil {
			return Poll{}, internal(err)
		}
		poll.IsClosed = true
	}
	votes, err := s.pollVotes(ctx, poll.ID)
	if err != nil {
		return Poll{}, err
	}
	return s.buildPollView(poll, votes, userID), nil
}

// attachPollsToMessages fills Message.Poll for every message that owns a poll.
// It costs at most two queries per page.
func (s *Service) attachPollsToMessages(ctx context.Context, viewerID string, items []Message) error {
	if s.polls == nil || len(items) == 0 {
		return nil
	}
	messageIDs := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			messageIDs = append(messageIDs, id)
		}
	}
	if len(messageIDs) == 0 {
		return nil
	}
	polls, err := s.polls.GetPollsByMessageIDs(ctx, messageIDs)
	if err != nil {
		return internal(err)
	}
	if len(polls) == 0 {
		return nil
	}
	pollIDs := make([]string, 0, len(polls))
	byMessage := make(map[string]Poll, len(polls))
	for _, poll := range polls {
		pollIDs = append(pollIDs, poll.ID)
		byMessage[poll.MessageID] = poll
	}
	votes, err := s.polls.ListVotesForPolls(ctx, pollIDs)
	if err != nil {
		return internal(err)
	}
	for idx := range items {
		poll, ok := byMessage[items[idx].ID]
		if !ok {
			continue
		}
		view := s.buildPollView(poll, votes[poll.ID], viewerID)
		items[idx].Poll = &view
	}
	return nil
}

// attachPollToMessage is attachPollsToMessages for a single message handle.
func (s *Service) attachPollToMessage(ctx context.Context, viewerID string, item *Message) error {
	if item == nil {
		return nil
	}
	list := []Message{*item}
	if err := s.attachPollsToMessages(ctx, viewerID, list); err != nil {
		return err
	}
	*item = list[0]
	return nil
}

func (s *Service) ensurePollReadable(ctx context.Context, poll Poll, userID string) error {
	if err := s.ensureChatReadableForViewer(ctx, poll.ChatID, userID); err != nil {
		return err
	}
	// A poll whose message was deleted or hidden is gone for good.
	if _, err := s.messages.GetMessageByID(ctx, poll.ChatID, poll.MessageID); err != nil {
		if errors.Is(err, ErrMessageNotFound) {
			return notFound("error.poll.not_found", err)
		}
		return mapChatOrMessageRepoError(err)
	}
	return nil
}

func (s *Service) pollVotes(ctx context.Context, pollID string) ([]PollVote, error) {
	if err := s.requirePolls(); err != nil {
		return nil, err
	}
	byPoll, err := s.polls.ListVotesForPolls(ctx, []string{pollID})
	if err != nil {
		return nil, internal(err)
	}
	return byPoll[pollID], nil
}

// buildPollView renders a poll for one viewer: it applies the effective
// close state, the hide_results gate and the deterministic per-viewer
// shuffle.
//
// hide_results rule:
//   - hidden = hide_results && !closed && the viewer has not voted yet.
//   - hidden  -> results_hidden=true, no results, no voters, no total_votes.
//   - voted   -> full tallies (that is the point of hiding them).
//   - closed  -> always revealed, whatever hide_results says.
func (s *Service) buildPollView(poll Poll, votes []PollVote, viewerID string) Poll {
	now := s.nowUTC()
	view := poll
	if view.Options == nil {
		view.Options = []PollOption{}
	}
	if view.CorrectOptionIDs == nil {
		view.CorrectOptionIDs = []string{}
	}
	view.MyOptionIDs = []string{}
	view.Closed = pollEffectivelyClosed(poll, now)

	mine := pollVoteByUser(votes, viewerID)
	if mine != nil {
		view.MyOptionIDs = append([]string{}, mine.OptionIDs...)
	}
	view.ResultsHidden = poll.HideResults && !view.Closed && len(view.MyOptionIDs) == 0

	if !view.ResultsHidden {
		counts := make(map[string]int, len(poll.Options))
		voters := make(map[string][]string, len(poll.Options))
		for _, option := range poll.Options {
			counts[option.ID] = 0
			if poll.ShowWhoVoted {
				voters[option.ID] = []string{}
			}
		}
		for _, vote := range votes {
			for _, optionID := range vote.OptionIDs {
				if _, ok := counts[optionID]; !ok {
					continue
				}
				counts[optionID]++
			}
			if poll.ShowWhoVoted {
				for _, optionID := range vote.OptionIDs {
					if _, ok := voters[optionID]; ok {
						voters[optionID] = append(voters[optionID], vote.UserID)
					}
				}
			}
		}
		total := len(votes)
		view.TotalVotes = &total
		results := make([]PollResult, 0, len(poll.Options))
		for _, option := range poll.Options {
			count := counts[option.ID]
			percent := 0
			if total > 0 {
				percent = int(mathRound(float64(count) * 100 / float64(total)))
			}
			results = append(results, PollResult{
				ID:      option.ID,
				Text:    option.Text,
				Votes:   count,
				Percent: percent,
			})
		}
		view.Results = results
		if poll.ShowWhoVoted {
			view.Voters = voters
		}
	}

	if poll.ShuffleOptions {
		order := pollShuffleOrder(poll.ID, viewerID, len(poll.Options))
		shuffled := make([]PollOption, 0, len(poll.Options))
		for _, idx := range order {
			shuffled = append(shuffled, poll.Options[idx])
		}
		view.Options = shuffled
		if len(view.Results) == len(poll.Options) {
			shuffledResults := make([]PollResult, 0, len(view.Results))
			for _, idx := range order {
				shuffledResults = append(shuffledResults, view.Results[idx])
			}
			view.Results = shuffledResults
		}
	}
	return view
}

// pollShuffleOrder is a deterministic permutation of [0,n) that depends only
// on the poll and the viewer, so every read returns the same order while two
// viewers see different ones.
func pollShuffleOrder(pollID, viewerID string, n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	if n <= 1 {
		return order
	}
	digest := sha256.Sum256([]byte(pollID + ":" + viewerID))
	seed := int64(binary.LittleEndian.Uint64(digest[:8]))
	rnd := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic display order, not crypto
	return rnd.Perm(n)
}

func pollEffectivelyClosed(poll Poll, now time.Time) bool {
	if poll.IsClosed {
		return true
	}
	return poll.ClosesAt != nil && !poll.ClosesAt.After(now)
}

func pollVoteByUser(votes []PollVote, userID string) *PollVote {
	for idx := range votes {
		if strings.EqualFold(votes[idx].UserID, userID) {
			return &votes[idx]
		}
	}
	return nil
}

func normalizePollOptions(raw []string) ([]PollOption, error) {
	if len(raw) < pollMinOptions || len(raw) > PollMaxOptions {
		return nil, invalidArg("error.poll.invalid_options")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]PollOption, 0, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(item)
		if text == "" || len([]rune(text)) > pollMaxOptionLen {
			return nil, invalidArg("error.poll.invalid_options")
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			return nil, invalidArg("error.poll.invalid_options")
		}
		seen[key] = struct{}{}
		out = append(out, PollOption{ID: uuid.NewString(), Text: text})
	}
	return out, nil
}

// resolveCorrectOptionIDs turns the 0-based index strings a client sends into
// generated option ids. The server owns the ids, so creation accepts indices
// only; everything after creation is expressed in ids.
func resolveCorrectOptionIDs(options []PollOption, raw []string, multiple bool) ([]string, error) {
	entries := make([]string, 0, len(raw))
	for _, item := range raw {
		if value := strings.TrimSpace(item); value != "" {
			entries = append(entries, value)
		}
	}
	if len(entries) == 0 {
		return []string{}, nil
	}
	if !multiple && len(entries) > 1 {
		return nil, invalidArg("error.poll.invalid_options")
	}
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		index, err := strconv.Atoi(entry)
		if err != nil || index < 0 || index >= len(options) {
			return nil, invalidArg("error.poll.invalid_options")
		}
		id := options[index].ID
		if _, ok := seen[id]; ok {
			return nil, invalidArg("error.poll.invalid_options")
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func validatePollAnswer(poll Poll, raw []string) ([]string, error) {
	known := make(map[string]struct{}, len(poll.Options))
	for _, option := range poll.Options {
		known[option.ID] = struct{}{}
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		id := strings.TrimSpace(item)
		if id == "" {
			return nil, invalidArg("error.poll.invalid_input")
		}
		if _, ok := known[id]; !ok {
			return nil, invalidArg("error.poll.invalid_input")
		}
		if _, ok := seen[id]; ok {
			return nil, invalidArg("error.poll.invalid_input")
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, invalidArg("error.poll.invalid_input")
	}
	if !poll.Multiple && len(out) != 1 {
		return nil, invalidArg("error.poll.invalid_input")
	}
	return out, nil
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

// mathRound keeps percent rounding away from float32 surprises without
// pulling in math for one call.
func mathRound(value float64) float64 {
	if value < 0 {
		return float64(int64(value - 0.5))
	}
	return float64(int64(value + 0.5))
}
