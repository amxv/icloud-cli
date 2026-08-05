package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

const maxDiscoveredFolders = 1000

func (client *Client) DiscoverFolders(ctx context.Context, request transport.AuthRequest) (transport.FolderDiscoveryResult, error) {
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.FolderDiscoveryResult{}, classifyConnectError(err)
	}
	defer plainConn.Close()

	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageTLS, err)
	}

	imapClient := imapclient.New(tlsConn, &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	})
	defer imapClient.Close()

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return transport.FolderDiscoveryResult{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(request.Username, string(request.Password)).Wait()
	}); err != nil {
		return transport.FolderDiscoveryResult{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}

	listed, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() ([]*imap.ListData, error) {
		return imapClient.List("", "*", nil).Collect()
	})
	if err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPList, err)
	}
	if len(listed) > maxDiscoveredFolders {
		return transport.FolderDiscoveryResult{}, transport.NewFailure(transport.StageIMAPList, transport.FailureUnavailable, false, errors.New("remote folder count exceeds safety bound"))
	}

	folders := make([]transport.RemoteFolder, 0, len(listed))
	for _, data := range listed {
		folder, err := remoteFolderFromList(data)
		if err != nil {
			return transport.FolderDiscoveryResult{}, transport.NewFailure(transport.StageIMAPList, transport.FailureUnavailable, false, err)
		}
		folders = append(folders, folder)
	}
	applySentFallback(folders)

	for index := range folders {
		if !folders[index].Selectable {
			continue
		}
		status, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.StatusData, error) {
			return imapClient.Status(folders[index].Name, &imap.StatusOptions{
				NumMessages: true,
				NumUnseen:   true,
				UIDNext:     true,
				UIDValidity: true,
			}).Wait()
		})
		if err != nil {
			return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPStatus, err)
		}
		value := transport.MailboxStatus{
			Mailbox:     folders[index].Name,
			UIDNext:     uint32(status.UIDNext),
			UIDValidity: status.UIDValidity,
		}
		if status.NumMessages != nil {
			value.Messages = *status.NumMessages
		}
		if status.NumUnseen != nil {
			value.Unseen = *status.NumUnseen
		}
		folders[index].Status = &value
	}

	sort.SliceStable(folders, func(left, right int) bool {
		leftName := strings.ToLower(folders[left].Name)
		rightName := strings.ToLower(folders[right].Name)
		if leftName == rightName {
			return folders[left].WireName < folders[right].WireName
		}
		return leftName < rightName
	})

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		return transport.FolderDiscoveryResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.FolderDiscoveryResult{
		Capabilities: capabilityStrings(postAuthCaps),
		Folders:      folders,
	}, nil
}

func remoteFolderFromList(data *imap.ListData) (transport.RemoteFolder, error) {
	if data == nil || invalidMailboxName(data.Mailbox) {
		return transport.RemoteFolder{}, errors.New("provider returned an invalid mailbox name")
	}
	delimiter := ""
	if data.Delim != 0 {
		delimiter = string(data.Delim)
	}
	attributes := sanitizeMailboxAttributes(data.Attrs)
	selectable := !hasMailboxAttribute(attributes, string(imap.MailboxAttrNoSelect)) &&
		!hasMailboxAttribute(attributes, string(imap.MailboxAttrNonExistent))
	subscription := "unknown"
	if hasMailboxAttribute(attributes, string(imap.MailboxAttrSubscribed)) {
		subscription = "subscribed"
	}
	kind, source := mailboxKind(data.Mailbox, attributes)
	parentName := mailboxParent(data.Mailbox, delimiter)
	oldName := strings.TrimSpace(data.OldName)
	return transport.RemoteFolder{
		Name:           data.Mailbox,
		WireName:       encodeMailboxWireName(data.Mailbox),
		OldName:        oldName,
		OldWireName:    encodeMailboxWireName(oldName),
		ParentName:     parentName,
		ParentWireName: encodeMailboxWireName(parentName),
		Delimiter:      delimiter,
		Attributes:     attributes,
		Kind:           kind,
		KindSource:     source,
		Selectable:     selectable,
		Subscription:   subscription,
	}, nil
}

func invalidMailboxName(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return true
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func sanitizeMailboxAttributes(values []imap.MailboxAttr) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		attribute := strings.TrimSpace(string(value))
		if attribute == "" || len(attribute) > 64 || strings.ContainsFunc(attribute, unicode.IsControl) {
			continue
		}
		key := strings.ToLower(attribute)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, attribute)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}

func hasMailboxAttribute(attributes []string, target string) bool {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute, target) {
			return true
		}
	}
	return false
}

func mailboxKind(name string, attributes []string) (string, string) {
	if strings.EqualFold(name, "INBOX") {
		return "inbox", "inbox"
	}
	for _, candidate := range []struct {
		attribute imap.MailboxAttr
		kind      string
	}{
		{imap.MailboxAttrSent, "sent"},
		{imap.MailboxAttrDrafts, "drafts"},
		{imap.MailboxAttrArchive, "archive"},
		{imap.MailboxAttrTrash, "trash"},
		{imap.MailboxAttrJunk, "junk"},
		{imap.MailboxAttrAll, "all"},
		{imap.MailboxAttrFlagged, "flagged"},
		{imap.MailboxAttrImportant, "important"},
	} {
		if hasMailboxAttribute(attributes, string(candidate.attribute)) {
			return candidate.kind, "attribute"
		}
	}
	return "other", ""
}

func applySentFallback(folders []transport.RemoteFolder) {
	for _, folder := range folders {
		if folder.Kind == "sent" {
			return
		}
	}
	for _, fallback := range []string{"Sent", "Sent Messages", "Sent Mail"} {
		for index := range folders {
			if strings.EqualFold(folders[index].Name, fallback) {
				folders[index].Kind = "sent"
				folders[index].KindSource = "fallback"
				return
			}
		}
	}
}

func mailboxParent(name, delimiter string) string {
	if delimiter == "" {
		return ""
	}
	index := strings.LastIndex(name, delimiter)
	if index <= 0 {
		return ""
	}
	return name[:index]
}
