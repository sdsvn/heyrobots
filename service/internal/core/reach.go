package core

// Reach is how far an outgoing action (an Effect) travels. Code, not the
// model, decides from the reach whether a person must approve the action.
type Reach string

const (
	// ReachInternal stays inside the Assistant: workspace files, notes,
	// memory and drafts.
	ReachInternal Reach = "internal"
	// ReachOwner reaches only the owner: an app reply, a push, an email to
	// the owner's own address.
	ReachOwner Reach = "owner"
	// ReachRequester is a reply in the requester's own conversation on the
	// Assistant's own channel, such as the app chat. It is never sent as the
	// owner, so it needs no approval even when the requester is a delegate.
	ReachRequester Reach = "requester"
	// ReachOthers reaches another person, such as an email, an invite reply,
	// a message, a comment or a pull request.
	ReachOthers Reach = "others"
	// ReachIrreversible cannot be undone: spending money, deleting data
	// outside the workspace, or publishing publicly.
	ReachIrreversible Reach = "irreversible"
)

// NeedsApproval reports whether an action of this reach needs the owner's
// approval before it is sent. Anything that reaches other people does, and
// an unknown reach does too, so a new kind fails closed.
func (r Reach) NeedsApproval() bool {
	switch r {
	case ReachInternal, ReachOwner, ReachRequester:
		return false
	default:
		return true
	}
}
