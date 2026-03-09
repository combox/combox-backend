package standalonechannel

import "strings"

type Channel struct {
	Kind             string
	IsPublic         bool
	CommentsEnabled  bool
	ReactionsEnabled bool
}

type Access struct {
	Role       string
	HasRole    bool
	IsBanned   bool
	CanPost    bool
	CanComment bool
	CanReact   bool
}

func IsChannel(channel Channel) bool {
	return strings.EqualFold(strings.TrimSpace(channel.Kind), "standalone_channel")
}

func IsOpen(channel Channel) bool {
	return IsChannel(channel) && channel.IsPublic
}

func ResolveAccess(channel Channel, role string, hasRole bool) Access {
	role = strings.TrimSpace(role)
	banned := hasRole && strings.EqualFold(role, "banned")
	canPost := false
	switch strings.ToLower(role) {
	case "owner", "admin":
		canPost = true
	}

	return Access{
		Role:       role,
		HasRole:    hasRole,
		IsBanned:   banned,
		CanPost:    !banned && canPost,
		CanComment: !banned && channel.CommentsEnabled,
		CanReact:   !banned && channel.ReactionsEnabled,
	}
}
