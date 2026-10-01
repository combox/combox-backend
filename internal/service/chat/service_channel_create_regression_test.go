package chat

import (
	"context"
	"testing"
)

// Regression for POST /api/private/v1/chats/{id}/channels returning 500 on
// voice channels (and GET /channels previously 500).
//
// Root cause: postgres CreateChannel allocated topic_number from
// COALESCE(next_topic_number, 2) only. Legacy groups (ETL/migrate inserts,
// groups created before 000023 backfill) have next_topic_number NULL or stale
// while channels up to N already exist, so the allocator reused topic 2 and
// hit idx_channel_topic_number_unique -> internal -> HTTP 500 for both
// "text" and "voice". The fix clamps allocation to MAX(topic_number)+1 with a
// retry on conflict; this test pins the service-level contract for both types.
func TestCreateChannelVoiceAndText(t *testing.T) {
	chatRepo := &memChatRepo{}
	svc, err := New(chatRepo, &memMsgRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	group, err := svc.CreateChat(ctx, CreateChatInput{
		UserID:    "u1",
		Title:     "Team",
		MemberIDs: []string{"u1", "u2"},
		Kind:      ChatKindGroup,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "text", input: "text", want: ChannelTypeText},
		{name: "voice", input: "voice", want: ChannelTypeVoice},
		{name: "voice upper+spaces", input: "  VOICE ", want: ChannelTypeVoice},
	}
	seenTopics := map[int]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := svc.CreateChannel(ctx, CreateChannelInput{
				UserID:      "u1",
				GroupChatID: group.ID,
				Title:       "channel-" + tc.name,
				ChannelType: tc.input,
			})
			if err != nil {
				t.Fatalf("create %s channel: %v", tc.name, err)
			}
			if created.ChannelType == nil || *created.ChannelType != tc.want {
				t.Fatalf("expected channel_type=%q, got %v", tc.want, created.ChannelType)
			}
			if created.Kind != "channel" {
				t.Fatalf("expected kind=channel, got %q", created.Kind)
			}
			if created.ParentChatID == nil || *created.ParentChatID != group.ID {
				t.Fatalf("expected parent %q, got %v", group.ID, created.ParentChatID)
			}
			if created.TopicNumber == nil || *created.TopicNumber < 2 {
				t.Fatalf("expected topic_number>=2, got %v", created.TopicNumber)
			}
			if prev, dup := seenTopics[*created.TopicNumber]; dup {
				t.Fatalf("duplicate topic_number %d (%q vs %q)", *created.TopicNumber, prev, tc.name)
			}
			seenTopics[*created.TopicNumber] = tc.name
		})
	}

	// GET /channels must not 500 and must list the created topics plus the
	// virtual General topic.
	items, err := svc.ListChannels(ctx, "u1", group.ID)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(items) != len(cases)+1 {
		t.Fatalf("expected %d topics (general+%d), got %d", len(cases)+1, len(cases), len(items))
	}
	if items[0].IsGeneral == nil || !*items[0].IsGeneral {
		t.Fatalf("expected first item to be the virtual General topic")
	}
	byType := map[string]int{}
	for _, item := range items[1:] {
		if item.ChannelType != nil {
			byType[*item.ChannelType]++
		}
	}
	if byType[ChannelTypeText] < 1 || byType[ChannelTypeVoice] < 2 {
		t.Fatalf("expected at least 1 text and 2 voice topics, got %v", byType)
	}

	// Invalid enum must be 400, not 500.
	if _, err := svc.CreateChannel(ctx, CreateChannelInput{
		UserID:      "u1",
		GroupChatID: group.ID,
		Title:       "bad",
		ChannelType: "video",
	}); err == nil {
		t.Fatalf("expected invalid channel_type to fail")
	} else if svcErr, ok := err.(*Error); !ok || svcErr.Code != CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for bad channel_type, got %v", err)
	}
}
