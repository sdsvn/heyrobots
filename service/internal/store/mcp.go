package store

import (
	"errors"
	"fmt"
	"heyrobots/service/internal/core"
	"time"

	"gorm.io/gorm"
)

// MCP transports.
const (
	MCPStdio = "stdio"
	MCPHTTP  = "http"
)

// MCP tool modes. A tool the owner hasn't reviewed has no mode and is off.
const (
	MCPToolRead    = "read"
	MCPToolOutward = "outward"
	MCPToolBlocked = "blocked"
)

// MCPServer is one of the owner's MCP servers. Its secrets live in the
// vault, never here.
type MCPServer struct {
	Name string `gorm:"primaryKey"`
	// Purpose says, in the owner's words, what the server is for.
	Purpose string
	// Title is the name the server gives itself, read when it is synced.
	Title     string
	Transport string
	// Command is the program and its arguments, as a JSON array, for a
	// stdio server.
	Command string
	// URL is the address of an http server.
	URL string
	// Trusted servers' results reach the worker directly instead of
	// through quarantine.
	Trusted     bool
	CreatedAt   time.Time
	CheckedAt   *time.Time
	StatusError string
}

// MCPTool is a tool one MCP server offers, with the owner's review of it.
type MCPTool struct {
	Server      string `gorm:"primaryKey"`
	Name        string `gorm:"primaryKey"`
	Title       string
	Description string
	// Schema is the tool's input schema as JSON.
	Schema string
	// Hash covers the description and schema the server sent last.
	Hash string
	// Mode is the owner's review: read, outward or blocked. Empty means
	// not reviewed yet.
	Mode string
	// Reach is how far an outward tool's calls travel.
	Reach string
	// ReviewedHash is the Hash the owner reviewed. When the server changes
	// a tool's description or schema, the hashes differ and the tool is off
	// until the owner reviews it again.
	ReviewedHash string
	ReadOnlyHint bool
	// DestructiveHint is the server's own claim; true when it says nothing.
	DestructiveHint bool
	SeenAt          time.Time
	// Removed is set when the server stopped offering the tool.
	Removed bool
}

// DefaultMode is how a tool starts: a read-only tool reads on its own,
// anything else asks the owner first on each call.
func DefaultMode(readOnly bool) (mode, reach string) {
	if readOnly {
		return MCPToolRead, ""
	}
	return MCPToolOutward, string(core.ReachOthers)
}

// Usable reports whether tasks may see the tool.
func (t MCPTool) Usable() bool {
	return !t.Removed && (t.Mode == MCPToolRead || t.Mode == MCPToolOutward) && t.ReviewedHash != "" && t.ReviewedHash == t.Hash
}

// NeedsReview reports whether the owner should look at the tool: it is new,
// or its description changed since the owner allowed it.
func (t MCPTool) NeedsReview() bool {
	return !t.Removed && t.Mode != MCPToolBlocked && t.ReviewedHash != t.Hash
}

// MCPServers lists the owner's MCP servers.
func (tx *Tx) MCPServers() ([]MCPServer, error) {
	var ss []MCPServer
	return ss, tx.db.Order("name").Find(&ss).Error
}

// MCPServer returns one MCP server.
func (tx *Tx) MCPServer(name string) (MCPServer, error) {
	var s MCPServer
	err := tx.db.First(&s, "name = ?", name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MCPServer{}, ErrNotFound
	}
	return s, err
}

// CreateMCPServer adds a server. It fails if the name is taken.
func (tx *Tx) CreateMCPServer(s MCPServer) error { return tx.db.Create(&s).Error }

// UpdateMCPServer changes some fields of a server.
func (tx *Tx) UpdateMCPServer(name string, updates map[string]any) error {
	res := tx.db.Model(&MCPServer{}).Where("name = ?", name).Updates(updates)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// DeleteMCPServer forgets a server and its tools.
func (tx *Tx) DeleteMCPServer(name string) error {
	if err := tx.db.Delete(&MCPTool{}, "server = ?", name).Error; err != nil {
		return err
	}
	res := tx.db.Delete(&MCPServer{}, "name = ?", name)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// MCPTools lists a server's tools, including removed ones.
func (tx *Tx) MCPTools(server string) ([]MCPTool, error) {
	var ts []MCPTool
	return ts, tx.db.Where("server = ?", server).Order("name").Find(&ts).Error
}

// SyncMCPTools records the tools a server offers now. A new tool, or one
// never reviewed, starts from what the server says: a read-only tool reads
// on its own; anything else waits for the owner's OK on each call. A known
// tool keeps its review, except that one whose description or schema
// changed goes back to asking. A switched-off tool stays off. Tools the
// server no longer offers are marked removed.
func (tx *Tx) SyncMCPTools(server string, offered []MCPTool, now time.Time) error {
	known, err := tx.MCPTools(server)
	if err != nil {
		return err
	}
	byName := map[string]MCPTool{}
	for _, t := range known {
		byName[t.Name] = t
	}
	seen := map[string]bool{}
	for _, t := range offered {
		seen[t.Name] = true
		t.Server, t.SeenAt, t.Removed = server, now, false
		if old, ok := byName[t.Name]; ok {
			t.Mode, t.Reach, t.ReviewedHash = old.Mode, old.Reach, old.ReviewedHash
		}
		switch {
		case t.Mode == "":
			t.Mode, t.Reach = DefaultMode(t.ReadOnlyHint)
			t.ReviewedHash = t.Hash
		case t.Mode != MCPToolBlocked && t.ReviewedHash != t.Hash:
			t.Mode, t.Reach, t.ReviewedHash = MCPToolOutward, string(core.ReachOthers), t.Hash
		}
		if err := tx.db.Save(&t).Error; err != nil {
			return err
		}
	}
	for _, t := range known {
		if !seen[t.Name] && !t.Removed {
			if err := tx.db.Model(&MCPTool{}).Where("server = ? AND name = ?", server, t.Name).Update("removed", true).Error; err != nil {
				return err
			}
		}
	}
	return tx.db.Model(&MCPServer{}).Where("name = ?", server).Updates(map[string]any{"checked_at": now, "status_error": ""}).Error
}

// ReviewMCPTool records the owner's review of a tool as it is now.
func (tx *Tx) ReviewMCPTool(server, name, mode, reach string) error {
	var t MCPTool
	err := tx.db.First(&t, "server = ? AND name = ?", server, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && t.Removed) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if mode != MCPToolOutward {
		reach = ""
	}
	return tx.db.Model(&MCPTool{}).Where("server = ? AND name = ?", server, name).
		Updates(map[string]any{"mode": mode, "reach": reach, "reviewed_hash": t.Hash}).Error
}

// LocalApp is an app on this machine the owner switched on.
type LocalApp struct {
	ID        string `gorm:"primaryKey"`
	EnabledAt time.Time
}

// LocalApps lists the local apps that are on.
func (tx *Tx) LocalApps() ([]LocalApp, error) {
	var as []LocalApp
	return as, tx.db.Find(&as).Error
}

// SetLocalApp switches a local app on or off.
func (tx *Tx) SetLocalApp(id string, on bool, now time.Time) error {
	if on {
		return tx.db.Save(&LocalApp{ID: id, EnabledAt: now}).Error
	}
	return tx.db.Delete(&LocalApp{}, "id = ?", id).Error
}

// The access all of a server's tools can be given at once.
const (
	AccessReads = "reads_on_own"
	AccessAsks  = "everything_asks"
	AccessOff   = "off"
)

// SetMCPAccess sets every tool of a server at once: read-only tools on
// their own and the rest asking first, everything asking first, or off.
func (tx *Tx) SetMCPAccess(server, access string) error {
	if access != AccessReads && access != AccessAsks && access != AccessOff {
		return fmt.Errorf("access is %s, %s or %s", AccessReads, AccessAsks, AccessOff)
	}
	ts, err := tx.MCPTools(server)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if t.Removed {
			continue
		}
		mode, reach := MCPToolBlocked, ""
		switch access {
		case AccessReads:
			mode, reach = DefaultMode(t.ReadOnlyHint)
		case AccessAsks:
			mode, reach = MCPToolOutward, string(core.ReachOthers)
		}
		if err := tx.ReviewMCPTool(server, t.Name, mode, reach); err != nil {
			return err
		}
	}
	return nil
}
