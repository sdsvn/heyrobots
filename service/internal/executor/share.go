package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/store"
)

// maxShared is the largest file an agent can share in a chat.
const maxShared = 50 << 20

type shareArgs struct {
	Path    string `json:"path" jsonschema:"the file's path in your task folder, such as report.pdf"`
	Caption string `json:"caption,omitempty" jsonschema:"one short line about it, shown above the file"`
}

// shareTool lets an agent hand a file it made to the person, as a download
// in the chat. The server keeps its own copy, so the file stays after the
// task's folder is gone.
func (r *run) shareTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "share_file",
		Description: "Share a file you made in your task folder with the person, such as a PDF, a spreadsheet or an image. It appears in the chat as a download. Use it whenever the result is a file, instead of describing the file.",
	}, func(_ agent.Context, a shareArgs) (ControlResult, error) {
		if core.ReplySource(r.mandate.Received.Source) != core.SourceApp {
			return ControlResult{Error: "files can only be shared in the app's chats"}, nil
		}
		if r.e.Files == "" {
			return ControlResult{Error: "sharing files isn't set up on this server"}, nil
		}
		ws, err := filepath.Abs(r.workspace())
		if err != nil {
			return ControlResult{}, err
		}
		src := filepath.Join(ws, filepath.Clean("/"+strings.TrimPrefix(a.Path, ws)))
		info, err := os.Lstat(src)
		switch {
		case err != nil:
			return ControlResult{Error: "there's no file at " + a.Path + " in your task folder"}, nil
		case !info.Mode().IsRegular():
			return ControlResult{Error: "only a plain file can be shared"}, nil
		case info.Size() == 0:
			return ControlResult{Error: "the file is empty"}, nil
		case info.Size() > maxShared:
			return ControlResult{Error: fmt.Sprintf("the file is larger than %d MB", maxShared>>20)}, nil
		}
		// A link inside the folder could point anywhere: only a file that
		// really lives in the task folder can be shared.
		real, err := filepath.EvalSymlinks(src)
		realWS, err2 := filepath.EvalSymlinks(ws)
		if err != nil || err2 != nil || !strings.HasPrefix(real, realWS+string(filepath.Separator)) {
			return ControlResult{Error: "only a file in your task folder can be shared"}, nil
		}
		src = real
		name := filepath.Base(src)
		rel := filepath.Join(ids.New("fl"), name)
		if err := copyFile(src, filepath.Join(r.e.Files, rel)); err != nil {
			return ControlResult{}, err
		}
		payload, _ := json.Marshal(store.SharedFile{Name: name, Size: info.Size(), Type: typeOf(src), Path: rel})
		caption := strings.TrimSpace(a.Caption)
		if caption == "" {
			caption = name
		}
		reach := core.ReachRequester
		if r.mandate.Role == core.RoleOwner {
			reach = core.ReachOwner
		}
		err = r.e.Store.Tx(func(tx *store.Tx) error {
			return tx.CreateEffect(store.Effect{TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: string(core.SourceApp),
				Kind: store.KindFile, Reach: string(reach), Body: shorten(caption, 300), Payload: string(payload), Status: store.EffectPending, CreatedAt: r.e.Now().UTC()})
		})
		if err != nil {
			return ControlResult{}, err
		}
		return ControlResult{OK: true}, nil
	})
}

// typeOf is a file's media type, from its name, else from its first bytes.
func typeOf(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return http.DetectContentType(head[:n])
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, maxShared)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
