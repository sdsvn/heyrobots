package executor

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/store"
)

type uiArgs struct {
	Text       string `json:"text" jsonschema:"what the UI says, in plain words, for where it can't be drawn"`
	Components string `json:"components" jsonschema:"a JSON array of A2UI v1.0 basic catalog components"`
}

// uiComponents are the basic catalog's components the chat draws: all but
// the video and audio players.
var uiComponents = map[string]bool{"Button": true, "Card": true, "CheckBox": true, "ChoicePicker": true, "Column": true,
	"DateTimeInput": true, "Divider": true, "Icon": true, "Image": true, "List": true, "Modal": true, "Row": true,
	"Slider": true, "Tabs": true, "Text": true, "TextField": true}

const maxUI = 64 << 10

// uiTool lets an agent reply with native UI, such as a card with choices
// and buttons. A tapped button comes back as the person's next message.
func (r *run) uiTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "show_ui",
		Description: "Show the person a small native UI in the chat, such as a card with options and buttons, when choosing or confirming is easier than typing. " +
			`components is a JSON array of A2UI v1.0 basic catalog components: each has an "id" and a "component" (Card, Column, Row, List, Text, Image, Icon, Divider, Button, TextField, CheckBox, ChoicePicker, Slider, DateTimeInput, Tabs, Modal); the one with id "root" is the top, and parents name children by id ("child": "id" or "children": ["id", ...]). ` +
			`Text has "text" (markdown: use "### " for a heading) and an optional "variant", "body" or "caption". A Button has "child" (a Text's id) and "action": {"event": {"name": "...", "context": {...}}}; tapping it sends the person's reply to you. Images need an https "url". ` +
			"text says the same thing in words, for where the UI can't be drawn. Only in the app's chats. " +
			"The card is your reply: when you report ready, leave result empty, or put only what the card doesn't already say.",
	}, func(_ agent.Context, a uiArgs) (ControlResult, error) {
		if core.ReplySource(r.mandate.Received.Source) != core.SourceApp {
			return ControlResult{Error: "native UI only shows in the app's chats; reply in text"}, nil
		}
		text := strings.TrimSpace(a.Text)
		if text == "" {
			return ControlResult{Error: "text is required: say in words what the UI shows"}, nil
		}
		messages, err := uiMessages(ids.New("ui"), a.Components)
		if err != nil {
			return ControlResult{Error: err.Error()}, nil
		}
		reach := core.ReachRequester
		if r.mandate.Role == core.RoleOwner {
			reach = core.ReachOwner
		}
		err = r.e.Store.Tx(func(tx *store.Tx) error {
			return tx.CreateEffect(store.Effect{TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: string(core.SourceApp),
				Kind: store.KindUI, Reach: string(reach), Body: shorten(text, 2000), Payload: string(messages), Status: store.EffectPending, CreatedAt: r.e.Now().UTC()})
		})
		if err != nil {
			return ControlResult{}, err
		}
		r.mu.Lock()
		r.showedUI = true
		r.mu.Unlock()
		return ControlResult{OK: true}, nil
	})
}

// uiMessages checks an agent's components against the chat's catalog and
// wraps them as the A2UI messages that draw one surface.
func uiMessages(surfaceID, components string) ([]byte, error) {
	if len(components) > maxUI {
		return nil, fmt.Errorf("the UI is larger than %d KB; keep it small", maxUI>>10)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(components), &list); err != nil {
		return nil, fmt.Errorf("components isn't a JSON array of objects: %v", err)
	}
	schemas, err := uiSchemas()
	if err != nil {
		return nil, err
	}
	byID := map[string]map[string]any{}
	for _, c := range list {
		id, _ := c["id"].(string)
		name, _ := c["component"].(string)
		switch {
		case id == "":
			return nil, fmt.Errorf("every component needs an id")
		case byID[id] != nil:
			return nil, fmt.Errorf("two components have the id %q", id)
		case !uiComponents[name]:
			return nil, fmt.Errorf("component %q (id %q) isn't one the chat can show", name, id)
		}
		if err := uiValid(schemas[name], c); err != nil {
			return nil, fmt.Errorf("component %q (%s) doesn't match the catalog: %v", id, name, err)
		}
		if url, ok := c["url"].(string); name == "Image" && (!ok || !strings.HasPrefix(url, "https://")) {
			return nil, fmt.Errorf("image %q needs an https url", id)
		}
		byID[id] = c
	}
	if byID["root"] == nil {
		return nil, fmt.Errorf(`one component needs the id "root"`)
	}
	for id, c := range byID {
		var kids []string
		if k, ok := c["child"].(string); ok {
			kids = append(kids, k)
		}
		if ks, ok := c["children"].([]any); ok {
			for _, k := range ks {
				s, _ := k.(string)
				kids = append(kids, s)
			}
		}
		for _, k := range kids {
			if byID[k] == nil {
				return nil, fmt.Errorf("%q names a child %q that isn't in the list", id, k)
			}
		}
	}
	return json.Marshal([]map[string]any{
		{"version": "1.0", "createSurface": map[string]any{"surfaceId": surfaceID, "catalogId": uiCatalog}},
		{"version": "1.0", "updateComponents": map[string]any{"surfaceId": surfaceID, "components": list}},
	})
}

const uiCatalog = "https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json"

// The A2UI v1.0 basic catalog and the types it shares, as a2ui_core 0.7.0
// ships them, so the server checks exactly what the app draws.
//
//go:embed a2ui/catalog.json a2ui/common_types.json
var a2uiFiles embed.FS

var uiSchemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	// Go's regexp has no XID classes: they are letters and letter numbers,
	// and for continuing also marks, digits and connectors.
	xid := strings.NewReplacer(`\p{XID_Start}`, `\p{L}\p{Nl}`, `\p{XID_Continue}`, `\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}`)
	c.UseRegexpEngine(func(s string) (jsonschema.Regexp, error) { return regexp.Compile(xid.Replace(s)) })
	for name, url := range map[string]string{"catalog.json": uiCatalog, "common_types.json": "https://a2ui.org/specification/v1_0/common_types.json"} {
		b, err := a2uiFiles.ReadFile("a2ui/" + name)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if err := c.AddResource(url, doc); err != nil {
			return nil, err
		}
	}
	out := map[string]*jsonschema.Schema{}
	for name := range uiComponents {
		s, err := c.Compile(uiCatalog + "#/components/" + name)
		if err != nil {
			return nil, fmt.Errorf("the UI catalog's %s: %w", name, err)
		}
		out[name] = s
	}
	return out, nil
})

func uiValid(s *jsonschema.Schema, c map[string]any) error {
	b, _ := json.Marshal(c)
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if err := s.Validate(v); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return fmt.Errorf("%s", strings.Join(leaves(ve), "; "))
		}
		return err
	}
	return nil
}

var english = message.NewPrinter(language.English)

// leaves are the innermost reasons a component failed, which say what to fix.
func leaves(e *jsonschema.ValidationError) []string {
	if len(e.Causes) == 0 {
		return []string{"/" + strings.Join(e.InstanceLocation, "/") + ": " + e.ErrorKind.LocalizedString(english)}
	}
	var out []string
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
