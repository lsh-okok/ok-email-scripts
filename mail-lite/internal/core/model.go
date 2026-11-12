package core

import (
	"encoding/json"
	"time"
)

type Account struct {
	ID              string   `json:"id"`
	PublicCode      string   `json:"public_code,omitempty"`
	Email           string   `json:"email"`
	Provider        string   `json:"provider"`
	MXDomain        string   `json:"mx_domain,omitempty"`
	MXRetentionDays *int     `json:"mx_retention_days,omitempty"`
	GroupID         string   `json:"group_id"`
	Status          string   `json:"status"`
	ClientID        string   `json:"client_id,omitempty"`
	RefreshToken    string   `json:"refresh_token,omitempty"`
	Password        string   `json:"password,omitempty"`
	Proxies         []string `json:"proxies,omitempty"`
	Note            string   `json:"note"`
	LastRefresh     string   `json:"last_refresh,omitempty"`
	RefreshError    string   `json:"refresh_error,omitempty"`
	Generation      int64    `json:"generation"`
	Version         int64    `json:"version"`
	HasCredential   bool     `json:"has_credential,omitempty"`
}
type Group struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ParentID string   `json:"parent_id"`
	Proxies  []string `json:"proxies"`
	// AccountCount is calculated for the admin group navigator and is not persisted.
	AccountCount int `json:"account_count,omitempty"`
}
type Platform struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Emails   []string `json:"sender_emails"`
	Domains  []string `json:"sender_domains"`
	Keywords []string `json:"subject_keywords"`
}
type Link struct {
	AccountEmail string `json:"account_email,omitempty"`
	AccountCode  string `json:"account_code,omitempty"`
	PlatformName string `json:"platform_name,omitempty"`
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	Recipient    string `json:"recipient"`
	Token        string `json:"token,omitempty"`
	TokenHash    string `json:"token_hash"`
	PlatformID   string `json:"platform_id"`
	ExpiresAt    string `json:"expires_at"`
	CreatedAt    string `json:"created_at"`
	URL          string `json:"url,omitempty"`
	JSONURL      string `json:"json_url,omitempty"`
}
type Settings struct {
	TimeZone       string `json:"time_zone"`
	ScanLimit      int    `json:"scan_limit"`
	RefreshEnabled bool   `json:"refresh_enabled"`
	RefreshCron    string `json:"refresh_cron"`
	RefreshDays    int    `json:"refresh_days"`
	PublicBaseURL  string `json:"public_base_url"`
}

func DefaultSettings() Settings {
	return Settings{TimeZone: "Asia/Shanghai", ScanLimit: 100, RefreshEnabled: true, RefreshCron: "0 2 * * *", RefreshDays: 30}
}

type Record struct {
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	Email      string          `json:"email,omitempty"`
	AccountID  string          `json:"account_id,omitempty"`
	ParentID   string          `json:"parent_id,omitempty"`
	Status     string          `json:"status,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	PlatformID string          `json:"platform_id,omitempty"`
	Recipient  string          `json:"recipient,omitempty"`
	TokenHash  string          `json:"token_hash,omitempty"`
	ExpiresAt  string          `json:"expires_at,omitempty"`
	PublicCode string          `json:"public_code,omitempty"`
	Data       json.RawMessage `json:"data"`
	Version    int64           `json:"version"`
}
type Event struct {
	Cursor  int64  `json:"cursor"`
	Record  Record `json:"record"`
	Deleted bool   `json:"deleted"`
}
type Page struct {
	Items    any `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}
type MessageRef struct {
	AccountID   string `json:"account_id"`
	Protocol    string `json:"protocol"`
	ID          string `json:"id,omitempty"`
	Folder      string `json:"folder"`
	Mailbox     string `json:"mailbox,omitempty"`
	UID         uint32 `json:"uid,omitempty"`
	UIDValidity uint32 `json:"uid_validity,omitempty"`
}
type Message struct {
	Ref                string              `json:"ref"`
	MessageID          string              `json:"message_id,omitempty"`
	Subject            string              `json:"subject"`
	From               string              `json:"from"`
	To                 string              `json:"to"`
	ReceivedAt         time.Time           `json:"received_at"`
	Folder             string              `json:"folder"`
	Preview            string              `json:"preview"`
	Text               string              `json:"text,omitempty"`
	HTML               string              `json:"html,omitempty"`
	Headers            map[string][]string `json:"-"`
	Source             MessageRef          `json:"-"`
	EnvelopeRecipients []string            `json:"-"`
}
type DeleteResult struct {
	Ref     string `json:"ref"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
type Node struct {
	Syncing       bool   `json:"syncing"`
	SnapshotDone  int64  `json:"snapshot_done"`
	SnapshotTotal int64  `json:"snapshot_total"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	IP            string `json:"ip"`
	Token         string `json:"token,omitempty"`
	Disabled      bool   `json:"disabled"`
	Revoked       bool   `json:"revoked"`
	LastSeen      string `json:"last_seen"`
	Cursor        int64  `json:"cursor"`
	Error         string `json:"error"`
}
type Job struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	Total      int      `json:"total"`
	Done       int      `json:"done"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors"`
	CreatedAt  string   `json:"created_at"`
	FinishedAt string   `json:"finished_at,omitempty"`
}
