package cli

// Read request structs intentionally live separately from write automation
// requests. They model request selection and content-expansion controls only;
// response encoding such as --json remains an ordinary output flag.

type accountListReadJSONRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type mailIdentityListReadJSONRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type mailIdentityShowReadJSONRequest struct {
	Identity string `json:"identity"`
}

type folderListReadJSONRequest struct {
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	IncludeMissing bool   `json:"include_missing,omitempty"`
	Verbose        bool   `json:"verbose,omitempty"`
}

type folderShowReadJSONRequest struct {
	Folder  string `json:"folder"`
	Verbose bool   `json:"verbose,omitempty"`
}

type folderRefreshReadJSONRequest struct {
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Verbose bool   `json:"verbose,omitempty"`
}

type messageListReadJSONRequest struct {
	Folder             string `json:"folder,omitempty"`
	Query              string `json:"query,omitempty"`
	Sender             string `json:"from,omitempty"`
	Participant        string `json:"participant,omitempty"`
	Subject            string `json:"subject,omitempty"`
	RFCMessageID       string `json:"rfc_id,omitempty"`
	AttachmentFilename string `json:"attachment_name,omitempty"`
	AttachmentType     string `json:"attachment_type,omitempty"`
	After              string `json:"after,omitempty"`
	Before             string `json:"before,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Unread             bool   `json:"unread,omitempty"`
	Read               bool   `json:"read,omitempty"`
	Flagged            bool   `json:"flagged,omitempty"`
	Unflagged          bool   `json:"unflagged,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

func (request messageListReadJSONRequest) searchFlags() mailSearchFlags {
	return mailSearchFlags{
		Query: request.Query, Sender: request.Sender, Participant: request.Participant,
		Subject: request.Subject, RFCMessageID: request.RFCMessageID,
		AttachmentFilename: request.AttachmentFilename, AttachmentType: request.AttachmentType,
		After: request.After, Before: request.Before, Cursor: request.Cursor,
		Unread: request.Unread, Read: request.Read, Flagged: request.Flagged, Unflagged: request.Unflagged,
	}
}

type mailSearchReadJSONRequest struct {
	Query              string `json:"query,omitempty"`
	Folder             string `json:"folder,omitempty"`
	Sender             string `json:"from,omitempty"`
	Participant        string `json:"participant,omitempty"`
	Subject            string `json:"subject,omitempty"`
	RFCMessageID       string `json:"rfc_id,omitempty"`
	AttachmentFilename string `json:"attachment_name,omitempty"`
	AttachmentType     string `json:"attachment_type,omitempty"`
	After              string `json:"after,omitempty"`
	Before             string `json:"before,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Unread             bool   `json:"unread,omitempty"`
	Read               bool   `json:"read,omitempty"`
	Flagged            bool   `json:"flagged,omitempty"`
	Unflagged          bool   `json:"unflagged,omitempty"`
	Result             string `json:"result,omitempty"`
	Remote             bool   `json:"remote,omitempty"`
	AllFolders         bool   `json:"all_folders,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

func (request mailSearchReadJSONRequest) searchFlags() mailSearchFlags {
	return mailSearchFlags{
		Folder: request.Folder, Sender: request.Sender, Participant: request.Participant,
		Subject: request.Subject, RFCMessageID: request.RFCMessageID,
		AttachmentFilename: request.AttachmentFilename, AttachmentType: request.AttachmentType,
		After: request.After, Before: request.Before, Cursor: request.Cursor,
		Unread: request.Unread, Read: request.Read, Flagged: request.Flagged, Unflagged: request.Unflagged,
	}
}

type threadListReadJSONRequest struct {
	Query              string `json:"query,omitempty"`
	Folder             string `json:"folder,omitempty"`
	Sender             string `json:"from,omitempty"`
	Participant        string `json:"participant,omitempty"`
	Subject            string `json:"subject,omitempty"`
	RFCMessageID       string `json:"rfc_id,omitempty"`
	AttachmentFilename string `json:"attachment_name,omitempty"`
	AttachmentType     string `json:"attachment_type,omitempty"`
	After              string `json:"after,omitempty"`
	Before             string `json:"before,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Unread             bool   `json:"unread,omitempty"`
	Read               bool   `json:"read,omitempty"`
	Flagged            bool   `json:"flagged,omitempty"`
	Unflagged          bool   `json:"unflagged,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

func (request threadListReadJSONRequest) searchFlags() mailSearchFlags {
	return mailSearchFlags{
		Query: request.Query, Folder: request.Folder, Sender: request.Sender, Participant: request.Participant,
		Subject: request.Subject, RFCMessageID: request.RFCMessageID,
		AttachmentFilename: request.AttachmentFilename, AttachmentType: request.AttachmentType,
		After: request.After, Before: request.Before, Cursor: request.Cursor,
		Unread: request.Unread, Read: request.Read, Flagged: request.Flagged, Unflagged: request.Unflagged,
	}
}

type threadShowReadJSONRequest struct {
	Target       string `json:"target"`
	Limit        int    `json:"limit,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	Full         bool   `json:"full,omitempty"`
	Verbose      bool   `json:"verbose,omitempty"`
	Format       string `json:"format,omitempty"`
	Headers      bool   `json:"headers,omitempty"`
	BodyOnly     bool   `json:"body_only,omitempty"`
	MetadataOnly bool   `json:"metadata_only,omitempty"`
}

func (request threadShowReadJSONRequest) outputOptions(jsonOutput bool) OutputOptions {
	return OutputOptions{
		JSON: jsonOutput, Full: request.Full, Verbose: request.Verbose, Format: request.Format,
		Headers: request.Headers, BodyOnly: request.BodyOnly, MetadataOnly: request.MetadataOnly,
	}
}

type messageShowReadJSONRequest struct {
	MessageID    string `json:"message_id"`
	Remote       bool   `json:"remote,omitempty"`
	Cache        bool   `json:"cache,omitempty"`
	Full         bool   `json:"full,omitempty"`
	Verbose      bool   `json:"verbose,omitempty"`
	Format       string `json:"format,omitempty"`
	Headers      bool   `json:"headers,omitempty"`
	BodyOnly     bool   `json:"body_only,omitempty"`
	MetadataOnly bool   `json:"metadata_only,omitempty"`
}

func (request messageShowReadJSONRequest) outputOptions(jsonOutput bool) OutputOptions {
	return OutputOptions{
		JSON: jsonOutput, Full: request.Full, Verbose: request.Verbose, Format: request.Format,
		Headers: request.Headers, BodyOnly: request.BodyOnly, MetadataOnly: request.MetadataOnly,
	}
}

type attachmentListReadJSONRequest struct {
	Target string `json:"target"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type operationListReadJSONRequest struct {
	Operation string `json:"operation,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type syncStatusReadJSONRequest struct {
	Folder string `json:"folder,omitempty"`
}

type checkpointListReadJSONRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
