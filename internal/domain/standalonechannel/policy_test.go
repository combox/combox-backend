package standalonechannel

import "testing"

func TestResolveAccessForViewer(t *testing.T) {
	access := ResolveAccess(Channel{
		Kind:             "standalone_channel",
		IsPublic:         true,
		CommentsEnabled:  true,
		ReactionsEnabled: true,
	}, "", false)

	if access.HasRole {
		t.Fatalf("expected anonymous viewer without role")
	}
	if access.CanPost {
		t.Fatalf("viewer should not be able to post root messages")
	}
	if !access.CanComment {
		t.Fatalf("viewer should be able to comment when comments are enabled")
	}
	if !access.CanReact {
		t.Fatalf("viewer should be able to react when reactions are enabled")
	}
}

func TestResolveAccessForBannedUser(t *testing.T) {
	access := ResolveAccess(Channel{
		Kind:             "standalone_channel",
		IsPublic:         true,
		CommentsEnabled:  true,
		ReactionsEnabled: true,
	}, "banned", true)

	if !access.IsBanned {
		t.Fatalf("expected banned access")
	}
	if access.CanPost || access.CanComment || access.CanReact {
		t.Fatalf("banned user must not have channel permissions")
	}
}
