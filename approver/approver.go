// Package approver defines the human-in-the-loop approval interface used by
// the broker to gate on-chain execution of agent-signed intents.
package approver

import (
	"context"

	"github.com/angqijiang-png/reins/intent"
)

// Approver asks a human to approve or reject a SignedIntent before execution.
//
// Implementations MUST block until a decision arrives or the supplied context
// is done. Returning (true, nil) means the human approved; (false, nil) means
// the human rejected; (false, err) means the request never resolved (typically
// a context cancellation or transport failure).
type Approver interface {
	// Request blocks until the human responds or ctx is done.
	Request(ctx context.Context, s intent.SignedIntent) (approved bool, err error)
}
