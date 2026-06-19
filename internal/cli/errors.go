package cli

import (
	"errors"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// asAPIError unwraps err into *client.APIError if the chain contains one. A thin wrapper over
// errors.As so the call sites in this package read cleanly.
func asAPIError(err error, target **client.APIError) bool {
	return errors.As(err, target)
}
