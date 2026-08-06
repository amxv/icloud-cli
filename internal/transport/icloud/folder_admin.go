package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

type mailboxNamespace struct {
	prefix    string
	delimiter string
}

func (client *Client) AdministerFolder(ctx context.Context, auth transport.AuthRequest, request transport.FolderAdminRequest) (transport.FolderAdminResult, error) {
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.FolderAdminResult{}, classifyConnectError(err)
	}
	defer plainConn.Close()

	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.FolderAdminResult{}, classifyFailure(transport.StageTLS, err)
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	defer closeIMAPClient(plainConn, imapClient)
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		return transport.FolderAdminResult{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	listed, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() ([]*imap.ListData, error) {
		return imapClient.List("", "*", nil).Collect()
	})
	if err != nil {
		return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPList, err)
	}
	if len(listed) > maxDiscoveredFolders {
		return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPList, transport.FailureUnavailable, false, errors.New("remote folder count exceeds safety bound"))
	}
	folders := make([]transport.RemoteFolder, 0, len(listed))
	for _, data := range listed {
		value, err := remoteFolderFromList(data)
		if err != nil {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPList, transport.FailureUnavailable, false, err)
		}
		folders = append(folders, value)
	}
	applySentFallback(folders)
	namespace, err := client.resolveMailboxNamespace(ctx, tlsConn, imapClient, postAuthCaps, folders)
	if err != nil {
		return transport.FolderAdminResult{}, err
	}

	result := transport.FolderAdminResult{Action: request.Action}
	source, sourceFound := findAdminFolder(folders, strings.TrimSpace(request.Mailbox))
	switch request.Action {
	case transport.FolderAdminCreate:
		if strings.TrimSpace(request.Mailbox) != "" {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPCreate, transport.FailureConfiguration, false, errors.New("create does not accept a source mailbox"))
		}
		name, normalizeErr := normalizeAdminMailboxName(request.NewMailbox, namespace)
		if normalizeErr != nil {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPCreate, transport.FailureConfiguration, false, normalizeErr)
		}
		if _, exists := findAdminFolder(folders, name); exists {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPCreate, transport.FailureConfiguration, false, errors.New("mailbox already exists"))
		}
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			return imapClient.Create(name, nil).Wait()
		}); err != nil {
			return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPCreate, err)
		}
		result.Folder = newAdminRemoteFolder(name, namespace)
	case transport.FolderAdminRename:
		if !sourceFound {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPRename, transport.FailureConfiguration, false, errors.New("source mailbox is missing"))
		}
		if protectedAdminFolder(source) {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPRename, transport.FailureConfiguration, false, errors.New("system mailbox cannot be renamed"))
		}
		name, normalizeErr := normalizeAdminMailboxName(request.NewMailbox, namespace)
		if normalizeErr != nil {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPRename, transport.FailureConfiguration, false, normalizeErr)
		}
		if existing, exists := findAdminFolder(folders, name); exists && existing.Name != source.Name {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPRename, transport.FailureConfiguration, false, errors.New("destination mailbox already exists"))
		}
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			return imapClient.Rename(source.Name, name, nil).Wait()
		}); err != nil {
			return transport.FolderAdminResult{}, &transport.FolderAdminTargetError{
				TargetMailbox: name,
				Err:           classifyFailure(transport.StageIMAPRename, err),
			}
		}
		result.PreviousName = source.Name
		result.Folder = renamedAdminRemoteFolder(source, name, namespace)
	case transport.FolderAdminDelete:
		if !sourceFound {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPDelete, transport.FailureConfiguration, false, errors.New("source mailbox is missing"))
		}
		if protectedAdminFolder(source) {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPDelete, transport.FailureConfiguration, false, errors.New("system mailbox cannot be deleted"))
		}
		if !source.Selectable {
			return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPDelete, transport.FailureConfiguration, false, errors.New("non-selectable mailbox deletion is not supported safely"))
		}
		status, statusErr := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.StatusData, error) {
			return imapClient.Status(source.Name, &imap.StatusOptions{NumMessages: true}).Wait()
		})
		if statusErr != nil {
			return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPStatus, statusErr)
		}
		if status.NumMessages != nil {
			result.SourceMessages = *status.NumMessages
		}
		if result.SourceMessages > 0 && !request.AllowNonEmpty {
			return transport.FolderAdminResult{}, &transport.FolderNonEmptyError{Messages: result.SourceMessages}
		}
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			return imapClient.Delete(source.Name).Wait()
		}); err != nil {
			return transport.FolderAdminResult{}, classifyFailure(transport.StageIMAPDelete, err)
		}
		result.Folder = source
		result.Deleted = true
	case transport.FolderAdminSubscribe, transport.FolderAdminUnsubscribe:
		if !sourceFound {
			return transport.FolderAdminResult{}, transport.NewFailure(folderSubscriptionStage(request.Action), transport.FailureConfiguration, false, errors.New("source mailbox is missing"))
		}
		stage := folderSubscriptionStage(request.Action)
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			if request.Action == transport.FolderAdminSubscribe {
				return imapClient.Subscribe(source.Name).Wait()
			}
			return imapClient.Unsubscribe(source.Name).Wait()
		}); err != nil {
			return transport.FolderAdminResult{}, classifyFailure(stage, err)
		}
		result.Folder = source
		if request.Action == transport.FolderAdminSubscribe {
			result.Folder.Subscription = "subscribed"
		} else {
			result.Folder.Subscription = "unsubscribed"
		}
	default:
		return transport.FolderAdminResult{}, transport.NewFailure(transport.StageIMAPList, transport.FailureConfiguration, false, errors.New("unsupported folder administration action"))
	}

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		// The provider command above is already confirmed. A later LOGOUT failure
		// must not turn a completed mailbox mutation into an ambiguous retry.
		return result, nil
	}
	return result, nil
}

func (client *Client) resolveMailboxNamespace(ctx context.Context, conn net.Conn, imapClient *imapclient.Client, caps imap.CapSet, folders []transport.RemoteFolder) (mailboxNamespace, error) {
	if caps.Has(imap.CapNamespace) || caps.Has(imap.CapIMAP4rev2) {
		data, err := runValueWithDeadline(ctx, conn, client.config.StageTimeout, func() (*imap.NamespaceData, error) {
			return imapClient.Namespace().Wait()
		})
		if err != nil {
			return mailboxNamespace{}, classifyFailure(transport.StageIMAPNamespace, err)
		}
		if data != nil && len(data.Personal) > 0 {
			// Prefer the root personal namespace. If the provider exposes only a
			// prefixed personal namespace, use that exact prefix instead.
			choice := data.Personal[0]
			for _, candidate := range data.Personal {
				if candidate.Prefix == "" {
					choice = candidate
					break
				}
			}
			delimiter := ""
			if choice.Delim != 0 {
				delimiter = string(choice.Delim)
			}
			return mailboxNamespace{prefix: choice.Prefix, delimiter: delimiter}, nil
		}
	}
	for _, value := range folders {
		if value.Delimiter != "" && strings.EqualFold(value.Name, "INBOX") {
			return mailboxNamespace{delimiter: value.Delimiter}, nil
		}
	}
	for _, value := range folders {
		if value.Delimiter != "" {
			return mailboxNamespace{delimiter: value.Delimiter}, nil
		}
	}
	return mailboxNamespace{}, nil
}

func normalizeAdminMailboxName(value string, namespace mailboxNamespace) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || name != value || len(name) > 1024 || !utf8.ValidString(name) || invalidMailboxName(name) {
		return "", errors.New("mailbox name is invalid")
	}
	if strings.EqualFold(name, "INBOX") {
		return "", errors.New("INBOX is reserved")
	}
	if namespace.prefix != "" && !strings.HasPrefix(name, namespace.prefix) {
		name = namespace.prefix + name
	}
	relative := strings.TrimPrefix(name, namespace.prefix)
	if relative == "" {
		return "", errors.New("mailbox name cannot be the namespace root")
	}
	if delimiter := namespace.delimiter; delimiter != "" {
		if strings.HasPrefix(relative, delimiter) || strings.HasSuffix(relative, delimiter) || strings.Contains(relative, delimiter+delimiter) {
			return "", errors.New("mailbox hierarchy contains an empty segment")
		}
	}
	return name, nil
}

func findAdminFolder(values []transport.RemoteFolder, name string) (transport.RemoteFolder, bool) {
	for _, value := range values {
		if value.Name == name || (strings.EqualFold(name, "INBOX") && strings.EqualFold(value.Name, "INBOX")) {
			return value, true
		}
	}
	return transport.RemoteFolder{}, false
}

func protectedAdminFolder(value transport.RemoteFolder) bool {
	return strings.EqualFold(value.Name, "INBOX") || value.Kind != "other"
}

func newAdminRemoteFolder(name string, namespace mailboxNamespace) transport.RemoteFolder {
	parent := mailboxParent(name, namespace.delimiter)
	return transport.RemoteFolder{
		Name:           name,
		WireName:       encodeMailboxWireName(name),
		ParentName:     parent,
		ParentWireName: encodeMailboxWireName(parent),
		Delimiter:      namespace.delimiter,
		Kind:           "other",
		Selectable:     true,
		Subscription:   "unknown",
	}
}

func renamedAdminRemoteFolder(source transport.RemoteFolder, name string, namespace mailboxNamespace) transport.RemoteFolder {
	value := source
	value.OldName = source.Name
	value.OldWireName = source.WireName
	value.Name = name
	value.WireName = encodeMailboxWireName(name)
	value.ParentName = mailboxParent(name, namespace.delimiter)
	value.ParentWireName = encodeMailboxWireName(value.ParentName)
	if namespace.delimiter != "" {
		value.Delimiter = namespace.delimiter
	}
	return value
}

func folderSubscriptionStage(action transport.FolderAdminAction) transport.Stage {
	if action == transport.FolderAdminSubscribe {
		return transport.StageIMAPSubscribe
	}
	return transport.StageIMAPUnsubscribe
}
