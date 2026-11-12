package api

import (
	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/changes"
	"heyrobots/service/internal/store"
)

// cardLinks is where a card opens once its change is made.
var cardLinks = map[string]string{"job": "plans", "rule": "rules", "setting": "settings"}

// cardItem shows a proposed change to jobs, rules or settings as a card;
// other effects are left as they are. While it waits, the card opens the
// approval and offers Confirm.
func cardItem(e store.Effect, item *gen.ThreadItem) {
	c, ok := changes.Of(e)
	if !ok {
		return
	}
	link := cardLinks[c.Kind]
	cc := gen.ChatCard{Kind: gen.ChatCardKind(c.Kind), Title: c.Title, Subtitle: c.Subtitle, Link: &link}
	if e.Status == store.EffectAwaitingApproval {
		link, action := "approval:"+apiEffectID(e.ID), "Confirm"
		cc.Link, cc.Action = &link, &action
	}
	if !cc.Kind.Valid() {
		return
	}
	item.Kind, item.Card = gen.Card, &cc
}
