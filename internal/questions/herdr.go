// The Prompter the server uses: the web app's send path (orch.Prompt), with its refusals mapped
// to ErrHold. orch.Prompt already refuses to type into a dialog or over a draft typed at the
// terminal, and marks failures that happened before typing with orch.ErrNotTyped; those are the
// cases where nothing reached the pane and a later attempt is safe.

package questions

import (
	"context"
	"errors"
	"fmt"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
)

// HerdrPrompter types answers with orch.Prompt through client.
func HerdrPrompter(client herdr.Client) Prompter {
	return func(ctx context.Context, paneID, text string) error {
		_, err := orch.Prompt(ctx, client, paneID, orch.PromptRequest{Text: text})
		var blocked *orch.BlockedError
		var draft *orch.DraftError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &blocked):
			return fmt.Errorf("%w: the agent is at a dialog", ErrHold)
		case errors.As(err, &draft):
			return fmt.Errorf("%w: the agent's input box holds a draft typed at the terminal", ErrHold)
		case errors.Is(err, orch.ErrNotTyped):
			return fmt.Errorf("%w: %w", ErrHold, err)
		default:
			return err
		}
	}
}
