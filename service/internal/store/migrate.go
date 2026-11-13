package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gorm.io/gorm"

	"heyrobots/service/internal/policy"
)

// migration is one numbered schema change. Each migration defines its own
// frozen copies of the tables it creates, so later changes to Go types never
// change what an old migration does.
type migration struct {
	version int
	name    string
	up      func(tx *gorm.DB) error
}

var migrations = []migration{
	{1, "devices and pairing codes", func(tx *gorm.DB) error {
		type device struct {
			ID           string `gorm:"primaryKey"`
			Name         string `gorm:"not null"`
			Platform     string
			Role         string `gorm:"not null"`
			DelegateName string
			TokenHash    []byte `gorm:"not null;uniqueIndex"`
			CreatedAt    time.Time
			LastSeenAt   *time.Time
			RevokedAt    *time.Time
		}
		type pairingCode struct {
			CodeHash     []byte `gorm:"primaryKey"`
			Role         string `gorm:"not null"`
			DelegateName string
			CreatedBy    string
			CreatedAt    time.Time
			ExpiresAt    time.Time `gorm:"not null;index"`
			UsedAt       *time.Time
		}
		return tx.AutoMigrate(&device{}, &pairingCode{})
	}},
	{2, "policy, threads, triggers, tickets, decisions and effects", func(tx *gorm.DB) error {
		type policyVersion struct {
			Version   int `gorm:"primaryKey;autoIncrement:false"`
			Body      string
			CreatedAt time.Time
		}
		type thread struct {
			ID             string `gorm:"primaryKey"`
			Source         string `gorm:"not null;uniqueIndex:idx_threads_source_key"`
			Key            string `gorm:"not null;uniqueIndex:idx_threads_source_key"`
			CreatedBy      string
			CreatedAt      time.Time
			UpdatedAt      time.Time `gorm:"index"`
			EvalID         string
			EvalLeaseUntil *time.Time
			EvalFailures   int
		}
		type trigger struct {
			ID           string `gorm:"primaryKey"`
			Source       string `gorm:"not null;uniqueIndex:idx_triggers_delivery"`
			DeliveryID   string `gorm:"not null;uniqueIndex:idx_triggers_delivery"`
			ActorSource  string
			ActorID      string
			Authenticity int
			IsBot        bool
			Addressed    bool
			ReceivedAll  bool
			Text         string
			At           time.Time
			CreatedAt    time.Time
			EvalState    string `gorm:"index"`
			EvalID       string
			Owned        bool
		}
		type triggerThread struct {
			TriggerID string `gorm:"primaryKey"`
			ThreadID  string `gorm:"primaryKey;index"`
		}
		type ticket struct {
			ID             string `gorm:"primaryKey"`
			Mandate        string
			Open           bool
			Stage          string
			StoppedBy      string
			OriginThreadID string
			CreatedAt      time.Time
			UpdatedAt      time.Time
		}
		type subscription struct {
			TicketID    string `gorm:"primaryKey"`
			ThreadID    string `gorm:"primaryKey;index"`
			ContextOnly bool
			CreatedAt   time.Time
		}
		type ticketInput struct {
			ID          uint   `gorm:"primaryKey"`
			TicketID    string `gorm:"index"`
			TriggerID   string
			CreatedAt   time.Time
			DeliveredAt *time.Time
		}
		type decision struct {
			ID            string `gorm:"primaryKey"`
			ThreadID      string `gorm:"index"`
			EvalID        string
			Kind          string
			TicketID      string
			TriggerIDs    string
			PolicyVersion int
			Note          string
			Reason        string
			CreatedAt     time.Time
		}
		type effect struct {
			ID        uint   `gorm:"primaryKey"`
			TicketID  string `gorm:"index"`
			ThreadID  string `gorm:"index"`
			Source    string
			Kind      string
			Reach     string
			Body      string
			Status    string `gorm:"index"`
			Attempts  int
			CreatedAt time.Time
			SentAt    *time.Time
		}
		if err := tx.AutoMigrate(&policyVersion{}, &thread{}, &trigger{}, &triggerThread{}, &ticket{},
			&subscription{}, &ticketInput{}, &decision{}, &effect{}); err != nil {
			return err
		}
		// Every Assistant starts with the default Policy as version 1.
		body, err := json.Marshal(policy.Default())
		if err != nil {
			return err
		}
		return tx.Create(&policyVersion{Version: 1, Body: string(body), CreatedAt: time.Now().UTC()}).Error
	}},
	{3, "assignments, effect approvals and hand-backs", func(tx *gorm.DB) error {
		type assignment struct {
			ID          string `gorm:"primaryKey"`
			TicketID    string `gorm:"index"`
			Kind        string
			Status      string `gorm:"index"`
			Recovered   bool
			StartedAt   time.Time
			LeaseUntil  time.Time
			EndedAt     *time.Time
			State       string
			Summary     string
			HumanAction string
			Result      string
		}
		type effect struct {
			Approval   string
			Action     string
			Recipients string
			Details    string
			Findings   string
		}
		type trigger struct {
			HandbackReason string
			HandedBackFrom string
		}
		if err := tx.AutoMigrate(&assignment{}); err != nil {
			return err
		}
		if err := tx.Table("effects").AutoMigrate(&effect{}); err != nil {
			return err
		}
		return tx.Table("triggers").AutoMigrate(&trigger{})
	}},
	{4, "outbox delivery, digests and model usage", func(tx *gorm.DB) error {
		type effect struct {
			NextAttemptAt *time.Time
			LeaseUntil    *time.Time
			LastError     string
			DecidedBy     string
			DecidedAt     *time.Time
			DigestID      string `gorm:"index"`
			Ref           string
		}
		type digest struct {
			ID        string `gorm:"primaryKey"`
			Body      string
			Count     int
			CreatedAt time.Time `gorm:"index"`
		}
		type usage struct {
			ID           uint      `gorm:"primaryKey"`
			At           time.Time `gorm:"index"`
			TicketID     string    `gorm:"index"`
			Model        string
			InputTokens  int64
			OutputTokens int64
			CostMicros   int64
		}
		if err := tx.Table("effects").AutoMigrate(&effect{}); err != nil {
			return err
		}
		return tx.AutoMigrate(&digest{}, &usage{})
	}},
	{5, "attention feed and pushes", func(tx *gorm.DB) error {
		type effect struct {
			AckedAt *time.Time
		}
		type push struct {
			Key string    `gorm:"primaryKey"`
			At  time.Time `gorm:"index"`
		}
		if err := tx.Table("effects").AutoMigrate(&effect{}); err != nil {
			return err
		}
		return tx.AutoMigrate(&push{})
	}},
	{6, "connected accounts, schedules and action payloads", func(tx *gorm.DB) error {
		type effect struct {
			Payload string
		}
		type account struct {
			Provider         string `gorm:"primaryKey"`
			Email            string
			Scopes           string
			AssistantAddress string
			ConnectedAt      time.Time
			Status           string
			StatusError      string
			MailCursor       string
			CalendarCursor   string
			CheckedAt        *time.Time
		}
		type schedule struct {
			ID        string `gorm:"primaryKey"`
			Name      string `gorm:"uniqueIndex"`
			Note      string
			Days      string
			Minute    int
			CreatedBy string
			CreatedAt time.Time
			LastRunAt *time.Time
			Enabled   bool
		}
		if err := tx.Table("effects").AutoMigrate(&effect{}); err != nil {
			return err
		}
		return tx.AutoMigrate(&account{}, &schedule{})
	}},
	{7, "answer choices for questions", func(tx *gorm.DB) error {
		type ticket struct {
			Choices string
		}
		return tx.Table("tickets").AutoMigrate(&ticket{})
	}},
	{8, "MCP servers and their tools", func(tx *gorm.DB) error {
		type mcpServer struct {
			Name        string `gorm:"primaryKey"`
			Purpose     string
			Transport   string
			Command     string
			URL         string
			Trusted     bool
			CreatedAt   time.Time
			CheckedAt   *time.Time
			StatusError string
		}
		type mcpTool struct {
			Server          string `gorm:"primaryKey"`
			Name            string `gorm:"primaryKey"`
			Title           string
			Description     string
			Schema          string
			Hash            string
			Mode            string
			Reach           string
			ReviewedHash    string
			ReadOnlyHint    bool
			DestructiveHint bool
			SeenAt          time.Time
			Removed         bool
		}
		return tx.AutoMigrate(&mcpServer{}, &mcpTool{})
	}},
	{9, "model provider per role", func(tx *gorm.DB) error {
		type modelRole struct {
			Role      string `gorm:"primaryKey"`
			Provider  string
			Model     string
			BaseURL   string
			PriceIn   float64
			PriceOut  float64
			HasPrice  bool
			UpdatedAt time.Time
		}
		return tx.AutoMigrate(&modelRole{})
	}},
	{10, "a team of agents", func(tx *gorm.DB) error {
		type agent struct {
			ID           string `gorm:"primaryKey"`
			Name         string `gorm:"uniqueIndex"`
			Purpose      string
			Instructions string
			Toolsets     string
			AnyToolset   bool
			Skills       string
			Provider     string
			Model        string
			BaseURL      string
			Capacity     int
			Color        string
			Badge        string
			Builtin      bool
			Enabled      bool
			CreatedAt    time.Time
			UpdatedAt    time.Time
		}
		type ticket struct {
			AgentID string `gorm:"index"`
		}
		type assignment struct {
			AgentID string `gorm:"index"`
		}
		if err := tx.AutoMigrate(&agent{}); err != nil {
			return err
		}
		if err := tx.Table("tickets").AutoMigrate(&ticket{}); err != nil {
			return err
		}
		if err := tx.Table("assignments").AutoMigrate(&assignment{}); err != nil {
			return err
		}
		// Today's setup becomes a team of one. Its capacity matches how many
		// tasks ran at once before.
		now := time.Now().UTC()
		if err := tx.Create(&agent{ID: "ag_general", Name: "General", Purpose: "Handles anything the other agents don't.",
			AnyToolset: true, Capacity: 3, Color: "violet", Badge: "star", Builtin: true, Enabled: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE tickets SET agent_id = 'ag_general' WHERE agent_id IS NULL OR agent_id = ''").Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE assignments SET agent_id = 'ag_general' WHERE agent_id IS NULL OR agent_id = ''").Error
	}},
	{11, "cached tokens", func(tx *gorm.DB) error {
		type usage struct {
			CachedTokens int64
		}
		return tx.Table("usages").AutoMigrate(&usage{})
	}},
	{12, "paused jobs", func(tx *gorm.DB) error {
		type assignment struct {
			PausedAt string
		}
		return tx.Table("assignments").AutoMigrate(&assignment{})
	}},
	{13, "job runs", func(tx *gorm.DB) error {
		type jobRun struct {
			Key       string `gorm:"primaryKey"`
			FirstSeen time.Time
			LastRunAt *time.Time
		}
		return tx.AutoMigrate(&jobRun{})
	}},
	{14, "chats and groups", func(tx *gorm.DB) error {
		type thread struct {
			AgentID string
			GroupID string
		}
		if err := tx.Table("threads").AutoMigrate(&thread{}); err != nil {
			return err
		}
		type group struct {
			ID        string `gorm:"primaryKey"`
			Name      string
			Members   string
			ThreadID  string
			CreatedAt time.Time
			RemovedAt *time.Time
		}
		type threadRead struct {
			DeviceID string `gorm:"primaryKey"`
			ThreadID string `gorm:"primaryKey"`
			ReadAt   time.Time
		}
		return tx.AutoMigrate(&group{}, &threadRead{})
	}},
	{15, "app catalogue: local apps and server titles", func(tx *gorm.DB) error {
		type mcpServer struct {
			Title string
		}
		if err := tx.Table("mcp_servers").AutoMigrate(&mcpServer{}); err != nil {
			return err
		}
		type localApp struct {
			ID        string `gorm:"primaryKey"`
			EnabledAt time.Time
		}
		return tx.AutoMigrate(&localApp{})
	}},
	{16, "machines that share one team", func(tx *gorm.DB) error {
		type trigger struct {
			Origin    string
			LeadAgent string
		}
		if err := tx.Table("triggers").AutoMigrate(&trigger{}); err != nil {
			return err
		}
		type effect struct {
			Origin   string
			RemoteID uint
			AgentID  string
		}
		if err := tx.Table("effects").AutoMigrate(&effect{}); err != nil {
			return err
		}
		type agent struct{ Home string }
		if err := tx.Table("agents").AutoMigrate(&agent{}); err != nil {
			return err
		}
		for _, q := range []string{
			"UPDATE triggers SET origin = '' WHERE origin IS NULL",
			"UPDATE triggers SET lead_agent = '' WHERE lead_agent IS NULL",
			"UPDATE effects SET origin = '' WHERE origin IS NULL",
			"UPDATE effects SET agent_id = '' WHERE agent_id IS NULL",
			"UPDATE agents SET home = '' WHERE home IS NULL",
		} {
			if err := tx.Exec(q).Error; err != nil {
				return err
			}
		}
		// Names stay unique among a machine's own agents; every machine
		// has a General.
		if err := tx.Exec("DROP INDEX IF EXISTS idx_agents_name").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE UNIQUE INDEX idx_agents_own_name ON agents(name) WHERE home = ''").Error; err != nil {
			return err
		}
		return tx.AutoMigrate(&Member{}, &ChatEvent{})
	}},
	{17, "an IT agent that sets up the team", func(tx *gorm.DB) error {
		now := time.Now().UTC()
		// Only when no agent here is called IT already.
		if err := tx.Exec(`INSERT INTO agents (id, name, purpose, instructions, toolsets, any_toolset, skills, provider, model, base_url,
			capacity, color, badge, builtin, enabled, created_at, updated_at, home)
			SELECT 'ag_it', 'IT', 'Sets up the team: finds and connects apps, adds skills and gives them to the agents who need them.', '',
				'["setup"]', false, '[]', '', '', '', 3, 'sky', 'bolt', true, true, ?, ?, ''
			WHERE NOT EXISTS (SELECT 1 FROM agents WHERE id = 'ag_it' OR (name = 'IT' AND home = ''))`, now, now).Error; err != nil {
			return err
		}
		// The owner's settings let setup work, as a new install's do.
		var last struct {
			Version int
			Body    string
		}
		if err := tx.Raw("SELECT version, body FROM policy_versions ORDER BY version DESC LIMIT 1").Scan(&last).Error; err != nil || last.Version == 0 {
			return err
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(last.Body), &p); err != nil {
			return err
		}
		ts, _ := p["toolsets"].([]any)
		if slices.Contains(ts, any("setup")) {
			return nil
		}
		p["toolsets"], p["version"] = append(ts, "setup"), last.Version+1
		body, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return tx.Exec("INSERT INTO policy_versions (version, body, created_at) VALUES (?, ?, ?)", last.Version+1, string(body), now).Error
	}},
	{18, "built-in agents work on three things at once", func(tx *gorm.DB) error {
		// One slot always stays free for a person's request, so three means
		// up to two background jobs and a message from you at once.
		return tx.Exec("UPDATE agents SET capacity = 3 WHERE builtin AND capacity < 3").Error
	}},
}

type schemaMigration struct {
	Version   int `gorm:"primaryKey;autoIncrement:false"`
	Name      string
	AppliedAt time.Time
}

func latestVersion() int { return migrations[len(migrations)-1].version }

func (s *Store) currentVersion() (int, error) {
	var v int
	err := s.db.Model(&schemaMigration{}).Select("COALESCE(MAX(version), 0)").Scan(&v).Error
	return v, err
}

func (s *Store) migrate(now time.Time) error {
	if err := s.db.AutoMigrate(&schemaMigration{}); err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	current, err := s.currentVersion()
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > latestVersion() {
		return fmt.Errorf("database is at schema version %d but this server knows only up to %d; use a newer heyrobotsd", current, latestVersion())
	}
	if current == latestVersion() {
		return nil
	}
	if current > 0 {
		if _, err := s.Backup(fmt.Sprintf("before-v%d", latestVersion()), now); err != nil {
			return fmt.Errorf("back up before migrating: %w", err)
		}
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := m.up(tx); err != nil {
				return err
			}
			return tx.Create(&schemaMigration{Version: m.version, Name: m.name, AppliedAt: now}).Error
		})
		if err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// Backup writes a consistent copy of the database into the backups folder
// and returns its path.
func (s *Store) Backup(label string, now time.Time) (string, error) {
	dir := filepath.Join(s.dataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("heyrobots-%s-%s.db", now.Format("20060102T150405Z"), label)
	path := filepath.Join(dir, name)
	// VACUUM INTO writes a consistent snapshot even while the database is in use.
	if err := s.db.Exec("VACUUM INTO ?", path).Error; err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// SchemaVersion returns the database's schema version.
func (s *Store) SchemaVersion() (int, error) { return s.currentVersion() }
