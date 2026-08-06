package cli

import (
	"context"
	"fmt"
	"io"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailLatestData struct {
	AccountID     string              `json:"account_id"`
	Threads       []maildomain.Thread `json:"threads"`
	Count         int                 `json:"count"`
	More          bool                `json:"more,omitempty"`
	SchemaVersion int64               `json:"schema_version"`
}

type publicThread struct {
	ID               string                  `json:"id"`
	AccountID        string                  `json:"account_id"`
	RootRFCMessageID string                  `json:"root_rfc_message_id,omitempty"`
	Subject          string                  `json:"subject,omitempty"`
	Snippet          string                  `json:"snippet,omitempty"`
	Participants     []string                `json:"participants,omitempty"`
	FolderIDs        []string                `json:"folder_ids,omitempty"`
	MessageCount     int                     `json:"message_count"`
	UnreadCount      int                     `json:"unread_count"`
	AttachmentCount  int                     `json:"attachment_count"`
	FirstMessageAt   any                     `json:"first_message_at,omitempty"`
	LastMessageAt    any                     `json:"last_message_at,omitempty"`
	Status           string                  `json:"status"`
	Reconstruction   string                  `json:"reconstruction"`
	RedirectedFrom   string                  `json:"redirected_from,omitempty"`
	Messages         []publicThreadMessage   `json:"messages"`
	Edges            []maildomain.ThreadEdge `json:"edges,omitempty"`
}

type publicThreadMessage struct {
	Message publicMessage      `json:"message"`
	Content messageContentData `json:"content"`
}

type mailThreadData struct {
	AccountID     string       `json:"account_id"`
	Thread        publicThread `json:"thread"`
	Count         int          `json:"count"`
	More          bool         `json:"more,omitempty"`
	SchemaVersion int64        `json:"schema_version"`
}

func newMailLatestCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "latest",
		Short:   "List the newest locally reconstructed conversations",
		Args:    cobra.NoArgs,
		Example: "  icloud mail latest\n  icloud mail latest --limit 50\n  icloud mail latest --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, threads, more, schemaVersion, err := state.mail.MailLatest(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailLatestData{
					AccountID: accountValue.ID, Threads: threads, Count: len(threads), More: more, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, thread := range threads {
				if err := renderThreadSummary(renderer, thread); err != nil {
					return err
				}
			}
			return renderer.Summary("threads", Field("account", accountValue.ID), Field("count", len(threads)), Field("more", fmt.Sprintf("%t", more)))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum reconstructed conversations to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailThreadCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "thread",
		Short:   "Read one locally reconstructed conversation",
		Args:    cobra.NoArgs,
		Example: "  icloud mail thread show thr_…\n  icloud mail thread show msg_… --full",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailThreadShowCommand(state))
	return command
}

func newMailThreadShowCommand(state *executionState) *cobra.Command {
	var options OutputOptions
	var limit int
	command := &cobra.Command{
		Use:     "show <thread-id|message-id>",
		Short:   "Show a bounded chronological page from a reconstructed conversation",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail thread show thr_…\n  icloud mail thread show msg_… --full\n  icloud mail thread show thr_… --format html --json\n  icloud mail thread show thr_… --headers --verbose",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			if err := ValidateOutputOptions(options, OutputSupport{
				JSON: true, Full: true, Verbose: true, Text: true, HTML: true, Raw: true,
				Headers: true, MetadataOnly: true,
			}); err != nil {
				return err
			}
			format, err := effectiveMessageFormat(options)
			if err != nil {
				return err
			}
			if format == maildomain.ContentRaw && !options.JSON {
				return NewAppError(ErrorUsage, "plain multi-message raw output is ambiguous; use --format raw --json or `mail message raw <message-id>`", false)
			}
			accountValue, page, sources, schemaVersion, err := state.mail.MailThread(
				cmd.Context(), state.global.Home, state.global.Account, args[0], limit,
			)
			if err != nil {
				return err
			}
			if options.JSON {
				public, buildErr := buildPublicThread(cmd.Context(), page.Thread, sources, options, format)
				if buildErr != nil {
					return buildErr
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailThreadData{
					AccountID: accountValue.ID, Thread: public, Count: len(public.Messages), More: page.More, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			if err := renderThreadSummary(renderer, page.Thread); err != nil {
				return err
			}
			for index, message := range page.Thread.Messages {
				if err := renderThreadMessage(renderer, message, options.MetadataOnly); err != nil {
					return err
				}
				if options.Verbose && message.Content != nil {
					for _, part := range message.Content.Parts {
						if err := renderMIMEPart(renderer, part); err != nil {
							return err
						}
					}
				}
				if options.Headers {
					if err := writeThreadHeaders(state.stdout, message); err != nil {
						return err
					}
				}
				if format == maildomain.ContentText || format == maildomain.ContentHTML {
					if _, err := fmt.Fprintf(state.stdout, "\nbody\nmessage=%s\n", message.ID); err != nil {
						return err
					}
					if err := sources[index].WriteBody(cmd.Context(), format, state.stdout); err != nil {
						return messageContentError(err)
					}
					if _, err := io.WriteString(state.stdout, "\n"); err != nil {
						return err
					}
				}
			}
			if options.Verbose {
				for _, edge := range page.Thread.Edges {
					if err := renderer.Record("thread-edge", edge.ID,
						Field("child", edge.ChildMessageID),
						Field("relation", edge.Relation),
						Field("ordinal", edge.Ordinal),
						Field("token", edge.Token),
						Field("parent", edge.ParentMessageID),
						Field("status", edge.Status),
					); err != nil {
						return err
					}
				}
			}
			return NewCompactTextRenderer(state.stdout).Summary("thread-page", Field("thread", page.Thread.ID), Field("count", len(page.Thread.Messages)), Field("more", fmt.Sprintf("%t", page.More)))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum chronological messages to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&options.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&options.Full, "full", false, "Include complete decoded text for every selected message")
	command.Flags().BoolVar(&options.Verbose, "verbose", false, "Include MIME part metadata and RFC graph edge evidence")
	command.Flags().StringVar(&options.Format, "format", "compact", "Content format: compact, text, html, or raw (raw requires --json)")
	command.Flags().BoolVar(&options.Headers, "headers", false, "Include decoded public headers for every selected message")
	command.Flags().BoolVar(&options.MetadataOnly, "metadata-only", false, "Show thread and message metadata without excerpts or bodies")
	return command
}

func renderThreadSummary(renderer *CompactTextRenderer, thread maildomain.Thread) error {
	return renderer.Record("thread", thread.ID,
		Field("subject", thread.CanonicalSubject),
		Field("messages", thread.MessageCount),
		Field("unread", thread.UnreadCount),
		Field("participants", thread.Participants),
		Field("folders", thread.FolderIDs),
		Field("attachments", thread.AttachmentCount),
		Field("last", thread.LastMessageAt),
		Field("redirected_from", thread.RedirectedFrom),
		Field("reconstruction", thread.Reconstruction),
	)
}

func renderThreadMessage(renderer *CompactTextRenderer, message maildomain.Message, metadataOnly bool) error {
	fields := []CompactField{
		Field("at", message.SortAt),
		Field("from", formatAddress(message.From)),
		Field("direction", message.Direction),
		Field("state", messageReadState(message)),
		Field("flagged", message.Flagged),
		Field("attachments", message.AttachmentCount),
	}
	if !metadataOnly {
		fields = append(fields, Field("excerpt", Excerpt(message.Snippet, DefaultExcerptRunes)))
	}
	return renderer.Record("message", message.ID, fields...)
}

func buildPublicThread(ctx context.Context, thread maildomain.Thread, sources []maildomain.ContentSource, options OutputOptions, format maildomain.ContentFormat) (publicThread, error) {
	messages := make([]publicThreadMessage, 0, len(thread.Messages))
	for index, message := range thread.Messages {
		content, err := buildJSONMessageContent(ctx, message, sources[index], options, format)
		if err != nil {
			return publicThread{}, err
		}
		publicMessage := sanitizeMessage(message)
		if options.MetadataOnly {
			publicMessage.Snippet = ""
		}
		messages = append(messages, publicThreadMessage{Message: publicMessage, Content: content})
	}
	var first, last any
	if !thread.FirstMessageAt.IsZero() {
		first = thread.FirstMessageAt
	}
	if !thread.LastMessageAt.IsZero() {
		last = thread.LastMessageAt
	}
	edges := []maildomain.ThreadEdge(nil)
	if options.Verbose || options.MetadataOnly {
		edges = append(edges, thread.Edges...)
	}
	snippet := thread.Snippet
	if options.MetadataOnly {
		snippet = ""
	}
	return publicThread{
		ID: thread.ID, AccountID: thread.AccountID, RootRFCMessageID: thread.RootRFCMessageID,
		Subject: thread.CanonicalSubject, Snippet: snippet,
		Participants: append([]string(nil), thread.Participants...), FolderIDs: append([]string(nil), thread.FolderIDs...),
		MessageCount: thread.MessageCount, UnreadCount: thread.UnreadCount, AttachmentCount: thread.AttachmentCount,
		FirstMessageAt: first, LastMessageAt: last, Status: thread.Status, Reconstruction: thread.Reconstruction,
		RedirectedFrom: thread.RedirectedFrom, Messages: messages, Edges: edges,
	}, nil
}

func writeThreadHeaders(writer io.Writer, message maildomain.Message) error {
	if _, err := fmt.Fprintf(writer, "\nheaders\nmessage=%s\n", message.ID); err != nil {
		return err
	}
	for _, field := range publicHeaders(message.Content) {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", field.Key, field.Value); err != nil {
			return err
		}
	}
	return nil
}
