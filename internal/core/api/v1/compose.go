package v1

// ComposeBodyKind classifies the body format of a ComposeRequest.
type ComposeBodyKind string

const (
	ComposeBodyPlain ComposeBodyKind = "plain"
	ComposeBodyHTML  ComposeBodyKind = "html"
)

// ComposeRequest prefills a new composer window.
//
// AccountID is optional and defaults to the user's default account.
// ReplyTo, when set, populates In-Reply-To / References from the referenced message.
type ComposeRequest struct {
	AccountID   string          `json:"accountId,omitempty"`
	To          []Address       `json:"to,omitempty"`
	Cc          []Address       `json:"cc,omitempty"`
	Bcc         []Address       `json:"bcc,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	Body        string          `json:"body,omitempty"`
	BodyKind    ComposeBodyKind `json:"bodyKind,omitempty"`
	Attachments []Attachment    `json:"attachments,omitempty"`
	ReplyTo     *MessageRef     `json:"replyTo,omitempty"`
}

// Composer opens a prefilled composer window. The user must click Send.
type Composer interface {
	OpenComposer(req ComposeRequest) error
}
