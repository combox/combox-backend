package chat

import standalonechanneldomain "combox-backend/internal/domain/standalonechannel"

type standaloneChannelAccess struct {
	Role       string
	HasRole    bool
	IsBanned   bool
	CanPost    bool
	CanComment bool
	CanReact   bool
}

func isStandaloneChannel(chatMeta Chat) bool {
	return standalonechanneldomain.IsChannel(standalonechanneldomain.Channel{
		Kind: chatMeta.Kind,
	})
}

func isOpenStandaloneChannel(chatMeta Chat) bool {
	return standalonechanneldomain.IsOpen(standalonechanneldomain.Channel{
		Kind:     chatMeta.Kind,
		IsPublic: chatMeta.IsPublic,
	})
}

func resolveStandaloneChannelPolicy(chatMeta Chat, role string, hasRole bool) standalonechanneldomain.Access {
	return standalonechanneldomain.ResolveAccess(standalonechanneldomain.Channel{
		Kind:             chatMeta.Kind,
		IsPublic:         chatMeta.IsPublic,
		CommentsEnabled:  chatMeta.CommentsEnabled,
		ReactionsEnabled: chatMeta.ReactionsEnabled,
	}, role, hasRole)
}
