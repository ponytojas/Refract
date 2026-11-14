package users

import "time"

type Purpose string

const (
	Verify Purpose = "verify" // confirms the email address
	Reset  Purpose = "reset"  // chooses a new password

	VerifyTTL = 24 * time.Hour
	ResetTTL  = time.Hour
)
