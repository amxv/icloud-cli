package cli

import (
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/spf13/cobra"
)

type doctorData struct {
	Reports       []account.DoctorReport `json:"reports"`
	Count         int                    `json:"count"`
	SchemaVersion int64                  `json:"schema_version"`
}

func newDoctorCommand(state *executionState) *cobra.Command {
	var all, live, jsonOutput, verbose bool
	command := &cobra.Command{
		Use:     "doctor [selector]",
		Short:   "Diagnose local account configuration and optional live connectivity",
		GroupID: "core",
		Args:    cobra.MaximumNArgs(1),
		Example: "  icloud doctor\n  icloud doctor Personal\n  icloud doctor --live --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			selector, err := selectedAccountSelector(state, args)
			if err != nil {
				return err
			}
			if all && strings.TrimSpace(selector) != "" {
				return NewAppError(ErrorUsage, "--all cannot be combined with an account selector", false)
			}
			if strings.TrimSpace(selector) == "" && !all {
				all = true
			}
			reports, schemaVersion, err := state.accounts.Doctor(cmd.Context(), state.global.Home, selector, all, live)
			if err != nil {
				return err
			}
			data := doctorData{Reports: reports, Count: len(reports), SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			if len(reports) == 0 {
				return renderer.Summary("doctor", Field("accounts", 0), Field("status", "configuration_required"))
			}
			for _, report := range reports {
				if err := renderDoctorReport(renderer, report, verbose); err != nil {
					return err
				}
			}
			return renderer.Summary("doctor", Field("accounts", len(reports)), Field("status", doctorOverallStatus(reports)))
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Diagnose every configured account (maximum 100)")
	command.Flags().BoolVar(&live, "live", false, "Resolve credentials and run read-only IMAP/SMTP validation")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include bounded protocol capability summaries")
	return command
}

func renderDoctorReport(renderer *CompactTextRenderer, report account.DoctorReport, verbose bool) error {
	fields := []CompactField{
		Field("status", report.Account.Status),
		Field("credential", doctorCredentialState(report)),
		Field("last_error", report.Account.LastErrorCode),
		Field("last_tested", report.Account.LastTestedAt),
		Field("last_healthy", report.Account.LastHealthyAt),
	}
	if report.Live != nil {
		fields = append(fields,
			Field("imap", protocolState(report.Live.IMAP)),
			Field("smtp", protocolState(report.Live.SMTP)),
		)
	}
	if verbose {
		fields = append(fields,
			Field("imap_caps", report.Account.IMAPCapabilities),
			Field("smtp_caps", report.Account.SMTPCapabilities),
		)
	}
	return renderer.Record("doctor", report.Account.ID, fields...)
}

func doctorCredentialState(report account.DoctorReport) string {
	if !report.CredentialConfigured || strings.TrimSpace(report.Account.CredentialScheme) == "" {
		return "missing"
	}
	return report.Account.CredentialScheme
}

func doctorOverallStatus(reports []account.DoctorReport) string {
	connected := 0
	for _, report := range reports {
		if report.Account.Status == account.StatusConnected {
			connected++
		}
	}
	switch {
	case connected == len(reports):
		return "healthy"
	case connected == 0:
		return "action_required"
	default:
		return fmt.Sprintf("partial_%d_of_%d", connected, len(reports))
	}
}
