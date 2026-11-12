package executor

import "google.golang.org/adk/v2/session"

func sessionGet(id string) *session.GetRequest {
	return &session.GetRequest{AppName: appName, UserID: "assistant", SessionID: id}
}
