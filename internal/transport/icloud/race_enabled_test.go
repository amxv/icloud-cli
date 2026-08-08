//go:build race

package icloud

import "time"

const (
	smtpFunctionalStageTimeout   = 10 * time.Second
	smtpCancellationPromptBudget = 2 * time.Second
)
