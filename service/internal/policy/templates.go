package policy

// RuleTemplate is a starter rule the owner can add from the app. Templates
// are only offered: adding one creates a normal rule, and accept mode
// still decides whether matching work starts or is only noted. The
// default mode, observe, notes it.
type RuleTemplate struct {
	Rule
	// Needs lists what the rule depends on, such as a connected Google
	// account.
	Needs []string `json:"needs,omitempty"`
	// Schedule is the time the rule wakes on, when it is time-based, as
	// days and HH:MM.
	ScheduleDays string `json:"schedule_days,omitempty"`
	ScheduleTime string `json:"schedule_time,omitempty"`
}

// RuleTemplates are the starter rules.
var RuleTemplates = []RuleTemplate{
	{
		Rule: Rule{Name: "morning-brief", Enabled: true,
			Text: "Each weekday at 8:00, give me a one-screen brief of today's calendar and anything urgent in my inbox."},
		Needs: []string{"google"}, ScheduleDays: "weekdays", ScheduleTime: "08:00",
	},
	{
		Rule: Rule{Name: "meeting-prep", Enabled: true,
			Text: "Thirty minutes before a meeting with other people, prepare a short note on who's attending and our last email thread with them."},
		Needs: []string{"google"},
	},
	{
		Rule: Rule{Name: "review-requests", Enabled: true,
			Text: "When a pull request requests my review, summarise it and flag risky parts."},
		Needs: []string{"github"},
	},
	{
		Rule: Rule{Name: "bills", Enabled: true,
			Text: "When I forward a bill, add the due date to my calendar and remind me two days before."},
		Needs: []string{"google"},
	},
}
