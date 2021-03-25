package dbop

import "errors"

// Sentinel errors.  To test these errors, use `errors.Is`.
var (
	ErrErrantTransactions = errors.New("detected errant transactions")
	ErrNoTopRunner        = errors.New("unable to determine the top runner")
	ErrTimeout            = errors.New("timeout")
)
