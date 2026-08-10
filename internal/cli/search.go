package cli

import (
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailSearchFlags struct {
	Query              string
	Folder             string
	Sender             string
	Participant        string
	Subject            string
	RFCMessageID       string
	AttachmentFilename string
	AttachmentType     string
	After              string
	Before             string
	Cursor             string
	Unread             bool
	Read               bool
	Flagged            bool
	Unflagged          bool
	VIP                bool
	Blocked            bool
}

type publicSearchHit struct {
	Kind             maildomain.SearchResultKind `json:"kind"`
	Message          *publicMessage              `json:"message,omitempty"`
	Thread           *maildomain.Thread          `json:"thread,omitempty"`
	MatchedMessageID string                      `json:"matched_message_id,omitempty"`
	MatchedField     string                      `json:"matched_field,omitempty"`
	Excerpt          string                      `json:"excerpt,omitempty"`
	Rank             float64                     `json:"rank"`
}

type mailSearchData struct {
	AccountID     string                     `json:"account_id"`
	Hits          []publicSearchHit          `json:"hits"`
	Count         int                        `json:"count"`
	More          bool                       `json:"more,omitempty"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	Query         maildomain.SearchQueryMeta `json:"query"`
	SchemaVersion int64                      `json:"schema_version"`
}

type mailRemoteSearchData struct {
	AccountID     string                       `json:"account_id"`
	Hits          []maildomain.RemoteSearchHit `json:"hits"`
	Count         int                          `json:"count"`
	More          bool                         `json:"more"`
	Partial       bool                         `json:"partial"`
	NextCursor    string                       `json:"next_cursor,omitempty"`
	Folders       int                          `json:"folders"`
	CandidateUIDs int                          `json:"candidate_uids"`
	SchemaVersion int64                        `json:"schema_version"`
}

type mailSearchRepairData struct {
	Summary       maildomain.SearchRepairSummary `json:"summary"`
	SchemaVersion int64                          `json:"schema_version"`
}

func newMailSearchCommand(state *executionState) *cobra.Command {
	var flags mailSearchFlags
	var jsonOutput bool
	var resultKind string
	var limit int
	var remote bool
	var allFolders bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:   "search [query]",
		Short: "Search synchronized mail locally or query bounded provider metadata explicitly",
		Args:  maximumReadArgsOrJSONInput(&input, 1),
		Example: "  icloud mail search invoice\n" +
			"  icloud mail search --from billing@example.com --unread\n" +
			"  icloud mail search launch --result threads --limit 20\n" +
			"  icloud mail search invoice --remote --folder inbox\n" +
			"  icloud mail search invoice --remote --folder inbox --cursor rcur_…\n" +
			"  icloud mail search invoice --remote --all-folders --cursor rcur_…\n" +
			"  icloud mail search --attachment-type application/pdf --json\n" +
			"  icloud mail search --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			request := mailSearchReadJSONRequest{Query: query, Result: string(maildomain.SearchMessages), Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request,
				"folder", "from", "participant", "subject", "rfc-id", "attachment-name", "attachment-type",
				"after", "before", "cursor", "unread", "read", "flagged", "unflagged", "vip", "blocked", "result", "remote", "all-folders", "limit"); err != nil {
				return err
			} else if used {
				query = request.Query
				flags = request.searchFlags()
				resultKind = request.Result
				remote = request.Remote
				allFolders = request.AllFolders
				limit = request.Limit
			}
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			kind := maildomain.SearchResultKind(strings.ToLower(strings.TrimSpace(resultKind)))
			if kind != maildomain.SearchMessages && kind != maildomain.SearchThreads {
				return NewAppError(ErrorUsage, "--result must be messages or threads", false)
			}
			options, err := flags.options(query, kind, limit)
			if err != nil {
				return err
			}
			if allFolders && !remote {
				return NewAppError(ErrorUsage, "--all-folders requires --remote", false)
			}
			if remote && (flags.VIP || flags.Blocked) {
				return NewAppError(ErrorUsage, "--vip and --blocked are local sender-preference filters and cannot be used with --remote", false)
			}
			if remote {
				accountValue, page, schemaVersion, err := state.mail.MailRemoteSearch(
					cmd.Context(), state.global.Home, state.global.Account, flags.Folder, allFolders, options,
				)
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailRemoteSearchData{
						AccountID: accountValue.ID, Hits: page.Hits, Count: page.Count,
						More: page.More, Partial: page.Partial, NextCursor: page.NextCursor, Folders: page.Folders,
						CandidateUIDs: page.CandidateUIDs, SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				renderer := NewCompactTextRenderer(state.stdout)
				for _, hit := range page.Hits {
					if err := renderRemoteSearchHit(renderer, hit); err != nil {
						return err
					}
				}
				return renderRemoteSearchSummary(renderer, accountValue.ID, page)
			}
			accountValue, _, page, schemaVersion, err := state.mail.MailSearch(
				cmd.Context(), state.global.Home, state.global.Account, flags.Folder, options,
			)
			if err != nil {
				return err
			}
			if jsonOutput {
				hits := make([]publicSearchHit, 0, len(page.Hits))
				for _, hit := range page.Hits {
					hits = append(hits, sanitizeSearchHit(hit))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSearchData{
					AccountID: accountValue.ID, Hits: hits, Count: len(hits), More: page.More,
					NextCursor: page.NextCursor, Query: page.Query, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, hit := range page.Hits {
				if err := renderSearchHit(renderer, hit); err != nil {
					return err
				}
			}
			return renderSearchSummary(renderer, accountValue.ID, page)
		},
	}
	addMailSearchFlags(command, &flags, true, false)
	command.Flags().StringVar(&resultKind, "result", string(maildomain.SearchMessages), "Result kind: messages or threads")
	command.Flags().BoolVar(&remote, "remote", false, "Query bounded provider metadata instead of local synchronized state")
	command.Flags().BoolVar(&allFolders, "all-folders", false, "Search every current selectable folder through bounded resumable fan-out")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum hits to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	command.AddCommand(newMailSearchRepairCommand(state))
	return command
}

func renderRemoteSearchHit(renderer *CompactTextRenderer, hit maildomain.RemoteSearchHit) error {
	state := "read"
	if !hit.Flags.Seen {
		state = "unread"
	}
	return renderer.Record("remote-hit", hit.Reference,
		Field("folder", hit.FolderName),
		Field("folder_id", hit.FolderID),
		Field("uid_validity", hit.UIDValidity),
		Field("uid", hit.UID),
		Field("from", formatAddress(hit.From)),
		Field("to", formatAddresses(hit.To)),
		Field("cc", formatAddresses(hit.CC)),
		Field("subject", hit.Subject),
		Field("at", hit.InternalDate),
		Field("state", state),
		Field("flagged", hit.Flags.Flagged),
		Field("match", hit.MatchedField),
		Field("cached_message", hit.CachedMessageID),
		Field("excerpt", Excerpt(hit.Snippet, DefaultExcerptRunes)),
	)
}

func renderRemoteSearchSummary(renderer *CompactTextRenderer, accountID string, page maildomain.RemoteSearchPage) error {
	return renderer.Summary("remote-results",
		Field("account", accountID),
		Field("count", fmt.Sprintf("%d", page.Count)),
		Field("more", fmt.Sprintf("%t", page.More)),
		Field("partial", fmt.Sprintf("%t", page.Partial)),
		Field("next_cursor", page.NextCursor),
		Field("folders", fmt.Sprintf("%d", page.Folders)),
		Field("candidate_uids", fmt.Sprintf("%d", page.CandidateUIDs)),
	)
}

func newMailSearchRepairCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "repair",
		Short:   "Rebuild a bounded batch of incomplete local body-search entries",
		Args:    cobra.NoArgs,
		Example: "  icloud mail search repair\n  icloud mail search repair --limit 100 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			var jsonRequest mailSearchRepairJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "limit"); err != nil {
				return err
			} else if used && jsonRequest.Limit != nil {
				limit = *jsonRequest.Limit
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, summary, schemaVersion, err := state.mail.MailSearchRepair(
				cmd.Context(), state.global.Home, state.global.Account, limit,
			)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSearchRepairData{
					Summary: summary, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("search-repair", accountValue.ID,
				Field("selected", fmt.Sprintf("%d", summary.Selected)),
				Field("repaired", fmt.Sprintf("%d", summary.Repaired)),
				Field("failed", fmt.Sprintf("%d", summary.Failed)),
				Field("remaining", fmt.Sprintf("%d", summary.Remaining)),
				Field("errors", fmt.Sprintf("%d", summary.Errors)),
			)
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum incomplete messages to repair (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func addMailSearchFlags(command *cobra.Command, flags *mailSearchFlags, includeFolder, includeQuery bool) {
	if includeQuery {
		command.Flags().StringVar(&flags.Query, "query", "", "Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID")
	}
	if includeFolder {
		command.Flags().StringVar(&flags.Folder, "folder", "", "Restrict matches to one folder kind, ID, name, or wire name")
	}
	command.Flags().StringVar(&flags.Sender, "from", "", "Restrict sender or Reply-To text")
	command.Flags().StringVar(&flags.Participant, "participant", "", "Restrict sender, Reply-To, To, or Cc participant text")
	command.Flags().StringVar(&flags.Subject, "subject", "", "Restrict subject text")
	command.Flags().StringVar(&flags.RFCMessageID, "rfc-id", "", "Match one RFC Message-ID")
	command.Flags().StringVar(&flags.AttachmentFilename, "attachment-name", "", "Restrict attachment filename text")
	command.Flags().StringVar(&flags.AttachmentType, "attachment-type", "", "Restrict attachment media type")
	command.Flags().StringVar(&flags.After, "after", "", "Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date")
	command.Flags().StringVar(&flags.Before, "before", "", "Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date")
	command.Flags().StringVar(&flags.Cursor, "cursor", "", "Continue from a next_cursor returned by the same query")
	command.Flags().BoolVar(&flags.Unread, "unread", false, "Include only messages with an unread eligible location")
	command.Flags().BoolVar(&flags.Read, "read", false, "Include only messages without an unread eligible location")
	command.Flags().BoolVar(&flags.Flagged, "flagged", false, "Include only messages with a flagged eligible location")
	command.Flags().BoolVar(&flags.Unflagged, "unflagged", false, "Include only messages without a flagged eligible location")
	command.Flags().BoolVar(&flags.VIP, "vip", false, "Include only messages from exact local VIP sender addresses")
	command.Flags().BoolVar(&flags.Blocked, "blocked", false, "Include only messages from exact local blocked sender addresses")
	command.MarkFlagsMutuallyExclusive("unread", "read")
	command.MarkFlagsMutuallyExclusive("flagged", "unflagged")
}

func (flags mailSearchFlags) options(query string, kind maildomain.SearchResultKind, limit int) (maildomain.SearchOptions, error) {
	if flags.Unread && flags.Read {
		return maildomain.SearchOptions{}, NewAppError(ErrorUsage, "--unread and --read cannot be used together", false)
	}
	if flags.Flagged && flags.Unflagged {
		return maildomain.SearchOptions{}, NewAppError(ErrorUsage, "--flagged and --unflagged cannot be used together", false)
	}
	after, err := parseSearchBoundary(flags.After)
	if err != nil {
		return maildomain.SearchOptions{}, NewAppError(ErrorUsage, "--after must be RFC3339 or YYYY-MM-DD", false)
	}
	before, err := parseSearchBoundary(flags.Before)
	if err != nil {
		return maildomain.SearchOptions{}, NewAppError(ErrorUsage, "--before must be RFC3339 or YYYY-MM-DD", false)
	}
	if after != nil && before != nil && !after.Before(*before) {
		return maildomain.SearchOptions{}, NewAppError(ErrorUsage, "--after must precede --before", false)
	}
	var unread, flagged, vip, blocked *bool
	if flags.Unread || flags.Read {
		value := flags.Unread
		unread = &value
	}
	if flags.Flagged || flags.Unflagged {
		value := flags.Flagged
		flagged = &value
	}
	if flags.VIP {
		value := true
		vip = &value
	}
	if flags.Blocked {
		value := true
		blocked = &value
	}
	return maildomain.SearchOptions{
		Query: strings.TrimSpace(query), Unread: unread, Flagged: flagged, VIP: vip, Blocked: blocked,
		Sender: flags.Sender, Participant: flags.Participant, Subject: flags.Subject,
		RFCMessageID: flags.RFCMessageID, AttachmentFilename: flags.AttachmentFilename,
		AttachmentType: flags.AttachmentType, After: after, Before: before,
		ResultKind: kind, Cursor: flags.Cursor, Limit: limit,
	}, nil
}

func parseSearchBoundary(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func sanitizeSearchHit(hit maildomain.SearchHit) publicSearchHit {
	result := publicSearchHit{
		Kind: hit.Kind, MatchedMessageID: hit.MatchedMessageID,
		MatchedField: hit.MatchedField, Excerpt: hit.Excerpt, Rank: hit.Rank,
	}
	if hit.Message != nil {
		message := sanitizeMessage(*hit.Message)
		result.Message = &message
	}
	if hit.Thread != nil {
		thread := *hit.Thread
		thread.Messages = nil
		thread.Edges = nil
		result.Thread = &thread
	}
	return result
}

func renderSearchHit(renderer *CompactTextRenderer, hit maildomain.SearchHit) error {
	if hit.Message != nil {
		message := *hit.Message
		return renderer.Record("search-hit", message.ID,
			Field("kind", "message"),
			Field("from", formatAddress(message.From)),
			Field("participants", searchMessageParticipants(message)),
			Field("subject", message.Subject),
			Field("at", message.SortAt),
			Field("state", messageReadState(message)),
			Field("flagged", message.Flagged),
			Field("vip", message.SenderPreference != nil && message.SenderPreference.VIP),
			Field("blocked", message.SenderPreference != nil && message.SenderPreference.Blocked),
			Field("match", hit.MatchedField),
			Field("excerpt", Excerpt(hit.Excerpt, DefaultExcerptRunes)),
		)
	}
	if hit.Thread != nil {
		thread := *hit.Thread
		return renderer.Record("search-hit", thread.ID,
			Field("kind", "thread"),
			Field("participants", thread.Participants),
			Field("subject", thread.CanonicalSubject),
			Field("last", thread.LastMessageAt),
			Field("messages", thread.MessageCount),
			Field("unread", thread.UnreadCount),
			Field("vip_senders", threadSenderPreferenceCount(thread.SenderPreferences, "vip")),
			Field("blocked_senders", threadSenderPreferenceCount(thread.SenderPreferences, "blocked")),
			Field("match", hit.MatchedField),
			Field("matched_message", hit.MatchedMessageID),
			Field("excerpt", Excerpt(hit.Excerpt, DefaultExcerptRunes)),
		)
	}
	return nil
}

func threadSenderPreferenceCount(summary *maildomain.ThreadSenderPreferenceSummary, kind string) int {
	if summary == nil {
		return 0
	}
	switch kind {
	case "vip":
		return summary.VIPSenders
	case "blocked":
		return summary.BlockedSenders
	default:
		return 0
	}
}

func renderSearchSummary(renderer *CompactTextRenderer, accountID string, page maildomain.SearchPage) error {
	return renderer.Summary("search-results",
		Field("account", accountID),
		Field("kind", page.Query.ResultKind),
		Field("count", len(page.Hits)),
		Field("more", fmt.Sprintf("%t", page.More)),
		Field("next_cursor", page.NextCursor),
		Field("sort", page.Query.Sort),
		Field("index", page.Query.IndexState),
		Field("pending", page.Query.PendingCount),
		Field("errors", page.Query.ErrorCount),
	)
}

func searchMessageParticipants(message maildomain.Message) []string {
	seen := map[string]struct{}{}
	values := []string{}
	appendAddress := func(address maildomain.Address) {
		email := strings.ToLower(strings.TrimSpace(address.Email))
		if email == "" {
			return
		}
		if _, ok := seen[email]; ok {
			return
		}
		seen[email] = struct{}{}
		values = append(values, email)
	}
	appendAddress(message.From)
	appendAddress(message.ReplyTo)
	for _, address := range message.To {
		appendAddress(address)
	}
	for _, address := range message.CC {
		appendAddress(address)
	}
	return values
}

func mailSearchFlagsActive(flags mailSearchFlags) bool {
	return strings.TrimSpace(flags.Query) != "" || strings.TrimSpace(flags.Sender) != "" ||
		strings.TrimSpace(flags.Participant) != "" || strings.TrimSpace(flags.Subject) != "" ||
		strings.TrimSpace(flags.RFCMessageID) != "" || strings.TrimSpace(flags.AttachmentFilename) != "" ||
		strings.TrimSpace(flags.AttachmentType) != "" || strings.TrimSpace(flags.After) != "" ||
		strings.TrimSpace(flags.Before) != "" || flags.Unread || flags.Read || flags.Flagged || flags.Unflagged || flags.VIP || flags.Blocked
}
