package chat

import (
	"context"
	"strings"
)

const (
	// chatExportMaxMessages caps an export at the newest N messages; older
	// messages are reported through ChatExport.Truncated.
	chatExportMaxMessages = 5000
	// chatExportPageSize is the internal paging step (ListMessages allows 100).
	chatExportPageSize = 100
)

// ExportChatHistory builds a JSON archive of everything the caller can still
// see in a chat: chat meta plus sender/timestamp/text/attachment summary/poll
// results per message. It pages through the normal read path so access
// checks, reaction sanitising, forward privacy, the history-clear watermark
// and poll visibility all apply unchanged.
func (s *Service) ExportChatHistory(ctx context.Context, userID, chatID string) (ChatExport, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return ChatExport{}, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatReadableForViewer(ctx, chatID, userID); err != nil {
		return ChatExport{}, err
	}
	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return ChatExport{}, mapChatOrMessageRepoError(err)
	}
	chatMeta.AvatarURL = s.resolveAvatarURL(ctx, chatMeta.AvatarURL)

	export := ChatExport{
		ExportedAt: s.nowUTC(),
		Chat:       chatMeta,
		Messages:   make([]ChatExportMessage, 0, chatExportPageSize),
	}

	cursor := ""
	for {
		page, err := s.ListMessages(ctx, ListMessagesInput{
			UserID: userID,
			ChatID: chatID,
			Limit:  chatExportPageSize,
			Cursor: cursor,
		})
		if err != nil {
			return ChatExport{}, err
		}
		for _, item := range page.Items {
			if len(export.Messages) >= chatExportMaxMessages {
				export.Truncated = true
				break
			}
			export.Messages = append(export.Messages, exportMessageFrom(item))
		}
		if export.Truncated || page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}

	if len(export.Messages) > 0 {
		ids := make([]string, 0, len(export.Messages))
		index := make(map[string]int, len(export.Messages))
		for idx, item := range export.Messages {
			ids = append(ids, item.ID)
			index[item.ID] = idx
		}
		attachments, err := s.messages.ListAttachmentSummaries(ctx, ids)
		if err != nil {
			return ChatExport{}, internal(err)
		}
		for id, summaries := range attachments {
			if idx, ok := index[id]; ok {
				export.Messages[idx].Attachments = summaries
			}
		}
	}

	export.MessageCount = len(export.Messages)
	return export, nil
}

func exportMessageFrom(item Message) ChatExportMessage {
	return ChatExportMessage{
		ID:                item.ID,
		CreatedAt:         item.CreatedAt,
		EditedAt:          item.EditedAt,
		SenderUserID:      item.UserID,
		SenderBotID:       item.SenderBotID,
		Text:              item.Content,
		ReplyToMessageID:  item.ReplyToMessageID,
		ForwardOriginName: item.ForwardOriginName,
		Reactions:         item.Reactions,
		Poll:              item.Poll,
	}
}
