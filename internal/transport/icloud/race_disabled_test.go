//go:build !race

package icloud

import "time"

const (
	smtpFunctionalStageTimeout   = time.Second
	smtpCancellationPromptBudget = 500 * time.Millisecond
)
