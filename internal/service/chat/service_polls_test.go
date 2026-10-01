package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"
)

type memPollRepo struct {
	polls     map[string]Poll
	byMessage map[string]string
	votes     map[string][]PollVote
	seq       int
}

func newMemPollRepo() *memPollRepo {
	return &memPollRepo{
		polls:     map[string]Poll{},
		byMessage: map[string]string{},
		votes:     map[string][]PollVote{},
	}
}

func (m *memPollRepo) CreatePoll(_ context.Context, poll Poll) (Poll, error) {
	m.seq++
	poll.ID = "poll-" + strconv.Itoa(m.seq)
	if poll.Options == nil {
		poll.Options = []PollOption{}
	}
	if poll.CorrectOptionIDs == nil {
		poll.CorrectOptionIDs = []string{}
	}
	m.polls[poll.ID] = poll
	m.byMessage[poll.MessageID] = poll.ID
	return poll, nil
}

func (m *memPollRepo) GetPoll(_ context.Context, pollID string) (Poll, error) {
	if poll, ok := m.polls[pollID]; ok {
		return poll, nil
	}
	return Poll{}, ErrPollNotFound
}

func (m *memPollRepo) GetPollsByMessageIDs(_ context.Context, messageIDs []string) ([]Poll, error) {
	out := make([]Poll, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		if pollID, ok := m.byMessage[messageID]; ok {
			out = append(out, m.polls[pollID])
		}
	}
	return out, nil
}

func (m *memPollRepo) ListVotesForPolls(_ context.Context, pollIDs []string) (map[string][]PollVote, error) {
	out := make(map[string][]PollVote, len(pollIDs))
	for _, pollID := range pollIDs {
		if votes, ok := m.votes[pollID]; ok {
			out[pollID] = append([]PollVote{}, votes...)
		}
	}
	return out, nil
}

func (m *memPollRepo) UpsertVote(_ context.Context, pollID, userID string, optionIDs []string, at time.Time) error {
	votes := m.votes[pollID]
	for idx := range votes {
		if votes[idx].UserID == userID {
			votes[idx].OptionIDs = append([]string{}, optionIDs...)
			votes[idx].VotedAt = at
			m.votes[pollID] = votes
			return nil
		}
	}
	m.votes[pollID] = append(votes, PollVote{
		UserID:    userID,
		OptionIDs: append([]string{}, optionIDs...),
		VotedAt:   at,
	})
	return nil
}

func (m *memPollRepo) ClosePoll(_ context.Context, pollID string) error {
	poll, ok := m.polls[pollID]
	if !ok {
		return ErrPollNotFound
	}
	poll.IsClosed = true
	m.polls[pollID] = poll
	return nil
}

func newPollService(t *testing.T) (*Service, *memChatRepo, *memMsgRepo, *memPollRepo) {
	t.Helper()
	chatRepo := &memChatRepo{}
	msgRepo := &memMsgRepo{}
	pollRepo := newMemPollRepo()
	svc, err := New(chatRepo, msgRepo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetPollRepository(pollRepo)
	return svc, chatRepo, msgRepo, pollRepo
}

func pollTestChat(t *testing.T, svc *Service) string {
	t.Helper()
	created, err := svc.CreateChat(context.Background(), CreateChatInput{
		UserID:    "u1",
		Title:     "Polls",
		MemberIDs: []string{"u2", "u3"},
	})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	return created.ID
}

func pollTestCreate(t *testing.T, svc *Service, chatID string, input CreatePollInput) Message {
	t.Helper()
	input.UserID = "u1"
	input.ChatID = chatID
	message, err := svc.CreatePoll(context.Background(), input)
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	return message
}

func TestPollCreateVoteAndResults(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)

	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Best framework?",
		Options:  []string{"Vue", "React", "Svelte"},
	})
	if message.Poll == nil {
		t.Fatalf("expected the created message to carry its poll")
	}
	if message.Content != "Best framework?" {
		t.Fatalf("poll message content must be the question, got %q", message.Content)
	}
	if len(message.Poll.Options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(message.Poll.Options))
	}
	seen := map[string]bool{}
	for _, option := range message.Poll.Options {
		if option.ID == "" || option.Text == "" {
			t.Fatalf("option must have an id and text, got %+v", option)
		}
		if seen[option.ID] {
			t.Fatalf("option ids must be unique, got %q", option.ID)
		}
		seen[option.ID] = true
	}
	if message.Poll.Closed || message.Poll.IsClosed || message.Poll.ResultsHidden {
		t.Fatalf("a fresh poll must be open and visible, got %+v", message.Poll)
	}
	if message.Poll.MyOptionIDs == nil {
		t.Fatalf("my_option_ids must serialise as [] before voting")
	}
	if message.Poll.CorrectOptionIDs == nil {
		t.Fatalf("correct_option_ids must serialise as [] for a regular poll")
	}

	// A second member reads the open poll before anyone voted.
	before, err := svc.GetPoll(ctx, "u2", message.Poll.ID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	if before.TotalVotes == nil || *before.TotalVotes != 0 {
		t.Fatalf("expected zero visible votes before voting, got %+v", before.TotalVotes)
	}
	if len(before.Results) != 3 {
		t.Fatalf("expected three tally rows, got %d", len(before.Results))
	}
	for _, result := range before.Results {
		if result.Votes != 0 || result.Percent != 0 {
			t.Fatalf("expected empty tallies, got %+v", result)
		}
	}

	first := message.Poll.Options[0].ID
	voted, err := svc.VotePoll(ctx, VotePollInput{UserID: "u2", PollID: message.Poll.ID, OptionIDs: []string{first}})
	if err != nil {
		t.Fatalf("vote: %v", err)
	}
	if voted.TotalVotes == nil || *voted.TotalVotes != 1 {
		t.Fatalf("expected one vote after voting, got %+v", voted.TotalVotes)
	}
	if len(voted.MyOptionIDs) != 1 || voted.MyOptionIDs[0] != first {
		t.Fatalf("expected my_option_ids to echo the ballot, got %+v", voted.MyOptionIDs)
	}
	if voted.Results[0].Votes != 1 || voted.Results[0].Percent != 100 {
		t.Fatalf("expected the voted option to hold 100%%, got %+v", voted.Results[0])
	}

	after, err := svc.GetPoll(ctx, "u3", message.Poll.ID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	if after.TotalVotes == nil || *after.TotalVotes != 1 {
		t.Fatalf("tallies must be visible for everyone by default, got %+v", after.TotalVotes)
	}
	if len(after.MyOptionIDs) != 0 {
		t.Fatalf("another viewer must not see someone else's ballot")
	}
}

func TestPollSingleAnswerRejectsMultipleOptions(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	chatID := pollTestChat(t, svc)
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Pick one",
		Options:  []string{"A", "B"},
	})

	_, err := svc.VotePoll(context.Background(), VotePollInput{
		UserID:    "u2",
		PollID:    message.Poll.ID,
		OptionIDs: []string{message.Poll.Options[0].ID, message.Poll.Options[1].ID},
	})
	assertPollErrorCode(t, err, CodeInvalidArgument)

	multi := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Pick many",
		Options:  []string{"A", "B", "C"},
		Multiple: true,
	})
	voted, err := svc.VotePoll(context.Background(), VotePollInput{
		UserID:    "u2",
		PollID:    multi.Poll.ID,
		OptionIDs: []string{multi.Poll.Options[0].ID, multi.Poll.Options[2].ID},
	})
	if err != nil {
		t.Fatalf("multiple answer vote: %v", err)
	}
	if voted.TotalVotes == nil || *voted.TotalVotes != 1 {
		t.Fatalf("two selections still count as one ballot, got %+v", voted.TotalVotes)
	}
	if voted.Results[0].Votes != 1 || voted.Results[2].Votes != 1 || voted.Results[1].Votes != 0 {
		t.Fatalf("unexpected multi tallies: %+v", voted.Results)
	}
}

func TestPollRevoteRejectedUnlessAllowed(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	chatID := pollTestChat(t, svc)
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Revotable?",
		Options:  []string{"A", "B"},
	})
	ctx := context.Background()
	if _, err := svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    message.Poll.ID,
		OptionIDs: []string{message.Poll.Options[0].ID},
	}); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	_, err := svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    message.Poll.ID,
		OptionIDs: []string{message.Poll.Options[1].ID},
	})
	assertPollErrorCode(t, err, CodeConflict)

	revotable := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question:      "Change your mind?",
		Options:       []string{"A", "B"},
		AllowRevoting: true,
	})
	if _, err := svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    revotable.Poll.ID,
		OptionIDs: []string{revotable.Poll.Options[0].ID},
	}); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	second, err := svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    revotable.Poll.ID,
		OptionIDs: []string{revotable.Poll.Options[1].ID},
	})
	if err != nil {
		t.Fatalf("revote must be allowed when the poll says so: %v", err)
	}
	if second.Results[0].Votes != 0 || second.Results[1].Votes != 1 {
		t.Fatalf("a revote must move the ballot, got %+v", second.Results)
	}
}

func TestPollClosedAndDeadlineRejectVotes(t *testing.T) {
	svc, _, _, pollRepo := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)

	closed := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Stopped early?",
		Options:  []string{"A", "B"},
	})
	if _, err := svc.ClosePoll(ctx, "u1", closed.Poll.ID); err != nil {
		t.Fatalf("close poll: %v", err)
	}
	view, err := svc.GetPoll(ctx, "u2", closed.Poll.ID)
	if err != nil {
		t.Fatalf("get closed poll: %v", err)
	}
	if !view.Closed || !view.IsClosed {
		t.Fatalf("closed poll must report closed, got %+v", view)
	}
	_, err = svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    closed.Poll.ID,
		OptionIDs: []string{view.Options[0].ID},
	})
	assertPollErrorCode(t, err, CodeConflict)

	// A deadline in the past is rejected when the poll is created.
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := svc.CreatePoll(ctx, CreatePollInput{
		UserID:   "u1",
		ChatID:   chatID,
		Question: "Deadline poll",
		Options:  []string{"A", "B"},
		ClosesAt: &past,
	}); err == nil {
		t.Fatalf("expected a past deadline to be rejected at creation")
	}

	// A deadline that elapses later ends the poll lazily: is_closed stays
	// false, but votes are refused and results are revealed.
	deadline, err := svc.CreateMessage(ctx, CreateMessageInput{UserID: "u1", ChatID: chatID, Content: "Deadline poll"})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	seeded, err := pollRepo.CreatePoll(ctx, Poll{
		ChatID:      chatID,
		MessageID:   deadline.ID,
		Question:    "Deadline poll",
		Options:     []PollOption{{ID: "opt-1", Text: "A"}, {ID: "opt-2", Text: "B"}},
		ClosesAt:    &past,
		HideResults: true,
		CreatedBy:   "u1",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	deadlineView, err := svc.GetPoll(ctx, "u3", seeded.ID)
	if err != nil {
		t.Fatalf("get deadline poll: %v", err)
	}
	if !deadlineView.Closed || deadlineView.IsClosed {
		t.Fatalf("an elapsed deadline must close the poll without is_closed, got %+v", deadlineView)
	}
	if deadlineView.ResultsHidden {
		t.Fatalf("an elapsed deadline must reveal results")
	}
	_, err = svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    seeded.ID,
		OptionIDs: []string{"opt-1"},
	})
	assertPollErrorCode(t, err, CodeConflict)
}

func TestPollHideResultsVisibility(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question:    "Secret until the end?",
		Options:     []string{"A", "B"},
		HideResults: true,
	})
	pollID := message.Poll.ID

	hidden, err := svc.GetPoll(ctx, "u2", pollID)
	if err != nil {
		t.Fatalf("get hidden poll: %v", err)
	}
	if !hidden.ResultsHidden || hidden.TotalVotes != nil || hidden.Results != nil || hidden.Voters != nil {
		t.Fatalf("an undecided viewer must not see tallies, got %+v", hidden)
	}
	if hidden.MyOptionIDs == nil {
		t.Fatalf("my_option_ids must stay an empty array")
	}

	voted, err := svc.VotePoll(ctx, VotePollInput{
		UserID:    "u2",
		PollID:    pollID,
		OptionIDs: []string{message.Poll.Options[0].ID},
	})
	if err != nil {
		t.Fatalf("vote: %v", err)
	}
	if voted.ResultsHidden || voted.TotalVotes == nil || *voted.TotalVotes != 1 {
		t.Fatalf("a voter must see the tallies right away, got %+v", voted)
	}

	stillHidden, err := svc.GetPoll(ctx, "u3", pollID)
	if err != nil {
		t.Fatalf("get poll as non voter: %v", err)
	}
	if !stillHidden.ResultsHidden || stillHidden.TotalVotes != nil {
		t.Fatalf("hide_results must keep hiding from non voters, got %+v", stillHidden)
	}

	if _, err := svc.ClosePoll(ctx, "u1", pollID); err != nil {
		t.Fatalf("close poll: %v", err)
	}
	revealed, err := svc.GetPoll(ctx, "u3", pollID)
	if err != nil {
		t.Fatalf("get closed hidden poll: %v", err)
	}
	if revealed.ResultsHidden {
		t.Fatalf("closing must reveal results whatever hide_results says")
	}
	if revealed.TotalVotes == nil || *revealed.TotalVotes != 1 {
		t.Fatalf("expected one vote after close, got %+v", revealed.TotalVotes)
	}
}

func TestPollShuffleIsStablePerViewer(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)
	options := make([]string, 0, PollMaxOptions)
	for i := 1; i <= PollMaxOptions; i++ {
		options = append(options, "Option "+strconv.Itoa(i))
	}
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question:       "Shuffle me",
		Options:        options,
		ShuffleOptions: true,
	})

	first, err := svc.GetPoll(ctx, "u2", message.Poll.ID)
	if err != nil {
		t.Fatalf("get poll: %v", err)
	}
	second, err := svc.GetPoll(ctx, "u2", message.Poll.ID)
	if err != nil {
		t.Fatalf("get poll again: %v", err)
	}
	if len(first.Options) != PollMaxOptions {
		t.Fatalf("expected %d options, got %d", PollMaxOptions, len(first.Options))
	}
	for idx := range first.Options {
		if first.Options[idx].ID != second.Options[idx].ID {
			t.Fatalf("shuffle must be stable for one viewer, pos %d", idx)
		}
	}
	other, err := svc.GetPoll(ctx, "u3", message.Poll.ID)
	if err != nil {
		t.Fatalf("get poll as other viewer: %v", err)
	}
	same := true
	for idx := range first.Options {
		if first.Options[idx].ID != other.Options[idx].ID {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("shuffle must differ between viewers")
	}
}

func TestPollHiddenFromNonMembers(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	ctx := context.Background()
	chatID := pollTestChat(t, svc)
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question: "Members only",
		Options:  []string{"A", "B"},
	})
	if _, err := svc.GetPoll(ctx, "stranger", message.Poll.ID); err == nil {
		t.Fatalf("expected a non member to be rejected")
	} else {
		assertPollErrorCode(t, err, CodeForbidden)
	}
	if _, err := svc.VotePoll(ctx, VotePollInput{UserID: "stranger", PollID: message.Poll.ID, OptionIDs: []string{message.Poll.Options[0].ID}}); err == nil {
		t.Fatalf("expected a non member vote to be rejected")
	}
}

func TestPollMessageDTOShape(t *testing.T) {
	svc, _, _, _ := newPollService(t)
	chatID := pollTestChat(t, svc)
	message := pollTestCreate(t, svc, chatID, CreatePollInput{
		Question:         "Shape?",
		Options:          []string{"A", "B"},
		ShowWhoVoted:     true,
		CorrectOptionIDs: []string{"1"},
	})

	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var decoded struct {
		Content string `json:"content"`
		Poll    *struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Options  []struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			} `json:"options"`
			CorrectOptionIDs []string `json:"correct_option_ids"`
			ResultsHidden    bool     `json:"results_hidden"`
			MyOptionIDs      []string `json:"my_option_ids"`
			TotalVotes       *int     `json:"total_votes"`
			Closed           bool     `json:"closed"`
		} `json:"poll"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if decoded.Poll == nil {
		t.Fatalf("poll must serialise on the message DTO, got %s", string(raw))
	}
	if decoded.Poll.ID == "" || decoded.Poll.Question != "Shape?" {
		t.Fatalf("unexpected poll payload: %+v", decoded.Poll)
	}
	if decoded.Poll.ResultsHidden || decoded.Poll.Closed {
		t.Fatalf("a fresh visible poll must serialise as open: %+v", decoded.Poll)
	}
	if decoded.Poll.MyOptionIDs == nil {
		t.Fatalf("my_option_ids must serialise as [] (got %s)", string(raw))
	}
	if decoded.Poll.TotalVotes == nil {
		t.Fatalf("total_votes must be present when results are visible")
	}
	if len(decoded.Poll.CorrectOptionIDs) != 1 || decoded.Poll.CorrectOptionIDs[0] != message.Poll.Options[1].ID {
		t.Fatalf("creation indices must resolve to option ids, got %+v vs %+v", decoded.Poll.CorrectOptionIDs, message.Poll.Options)
	}
	if decoded.Poll.Options[0].ID == decoded.Poll.Options[1].ID {
		t.Fatalf("option ids must be distinct")
	}
}

func assertPollErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q", code)
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected a chat service error, got %v", err)
	}
	if svcErr.Code != code {
		t.Fatalf("expected code %q, got %q", code, svcErr.Code)
	}
}
