package notify

// Snapshot defines notification_events.snapshot JSONB, written by finding transactions in db and
// read by server delivery/filtering. It belongs to the notification domain; db only serialises it.
// Store finding fields rather than querying later because names, severity, and status may change;
// notifications must reflect the conclusion at event time, not misleading later downgrades. This
// also avoids JOINs on findings/tasks/assets during fan-out and rendering.
//
//
type Snapshot struct {
	// Event types: finding_created / finding_status_changed.
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// Nonempty only for kind=finding_status_changed.
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item is one finding prepared for channel rendering.
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets contains resolved display names such as domains/IPs, populated by server; this package
	// does not access the database.
	Assets []string
	// DetailURL links to finding details; omit it when public_base_url is unset.
	DetailURL string
	// For status-change events only; render both nonempty values as "Pending → Fixed".
	FromStatus string
	ToStatus   string
}

// IsStatusChange reports whether this is a status-change event.
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title prefers manually assigned name, then vulnclass, then a placeholder; never returns an empty
// title.
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(Untitled finding)"
}

// Message contains a complete channel send.
type Message struct {
	// One item for individual delivery, an entire batch for digests. Empty slices are invalid; callers
	// must provide at least one item.
	Items []Item
	// Batch=true renders digest titles, time windows, and counts.
	Batch bool
	// WindowMinutes is the digest interval used for "Last N minutes" when Batch=true. Pass it
	// explicitly instead of calculating time.Since to keep rendering deterministic and testable.
	WindowMinutes int
	// HomeURL is the dashboard address (global public_base_url); omit the dashboard link when empty.
	HomeURL string
}
