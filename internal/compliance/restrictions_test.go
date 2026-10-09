package compliance

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRestrictionLevels(t *testing.T) {
	want := map[string]Level{"RESTRICT": LevelRestricted, "SUSPEND": LevelSuspended, "OFFBOARD": LevelOffboarded,
		"CONFIRMED_FRAUD": LevelOffboarded, "CLEARED": LevelNone, "EDD_CONDITIONS": LevelNone}
	for d := range Decisions {
		if LevelForDecision(d) != want[d] {
			t.Errorf("%s -> %q, want %q", d, LevelForDecision(d), want[d])
		}
		// exactly the checker-required decisions restrict
		if (LevelForDecision(d) != LevelNone) != CheckerRequired[d] {
			t.Errorf("%s: restricting decisions must be exactly the maker-checker ones", d)
		}
	}
	if Max(LevelRestricted, LevelOffboarded) != LevelOffboarded || Max(LevelSuspended, LevelRestricted) != LevelSuspended ||
		Max(LevelNone, LevelRestricted) != LevelRestricted || Max(LevelNone, LevelNone) != LevelNone {
		t.Fatal("highest level must win")
	}
}

// The projection's level and lift vocabularies match the migration.
func TestRestrictionVocabularyMatchesMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/20261009170200_compliance_restrictions.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, l := range []Level{LevelRestricted, LevelSuspended, LevelOffboarded} {
		if !strings.Contains(s, "'"+string(l)+"'") {
			t.Errorf("level %s missing from the migration", l)
		}
	}
	for _, r := range []string{"CLEARED", "EDD_CONDITIONS", "SUPERSEDED", "SUBJECT_UNLINKED"} {
		if !strings.Contains(s, "'"+r+"'") {
			t.Errorf("lift reason %s missing from the migration", r)
		}
	}
	for _, v := range []string{"'CAMPAIGN_REVIEW'", "'CAMPAIGN_ESCALATION'"} {
		if !strings.Contains(s, v) {
			t.Errorf("%s missing from the migration", v)
		}
	}
}

func TestCampaignReviewEscalationTrigger(t *testing.T) {
	const camp, rev, owner = "0192f000-0000-7000-8000-0000000000c1", "0192f000-0000-7000-8000-0000000000c2", "0192f000-0000-7000-8000-0000000000c3"
	raw, _ := json.Marshal(map[string]string{"campaign_id": camp, "owner_user_id": owner, "review_id": rev})
	tr, err := TriggerFromEvent(EvCampaignReviewEscalated, raw)
	if err != nil {
		t.Fatal(err)
	}
	if tr.CaseType != "CAMPAIGN_REVIEW" || tr.Source != "CAMPAIGN_ESCALATION" || tr.ReasonCode != "CAMPAIGN_REVIEW_ESCALATED" {
		t.Fatalf("trigger: %+v", tr)
	}
	if len(tr.Links) != 3 || tr.Links[0] != (LinkInput{"CAMPAIGN", camp, "PRIMARY_SUBJECT"}) ||
		tr.Links[1] != (LinkInput{"USER", owner, "RELATED_SUBJECT"}) || tr.Links[2] != (LinkInput{"CAMPAIGN_REVIEW", rev, "RELATED_OBJECT"}) {
		t.Fatalf("links: %+v", tr.Links)
	}
	if d := validateLinks(tr.Links); len(d) != 0 {
		t.Fatalf("links do not validate: %v", d)
	}
	raw, _ = json.Marshal(map[string]string{"campaign_id": camp, "owner_organisation_id": owner, "review_id": rev})
	if tr, err = TriggerFromEvent(EvCampaignReviewEscalated, raw); err != nil || tr.Links[1].SubjectType != "ORGANISATION" {
		t.Fatalf("organisation owner: %+v %v", tr, err)
	}
	for _, bad := range []map[string]string{
		{"campaign_id": camp, "review_id": rev},
		{"campaign_id": camp, "owner_user_id": owner, "owner_organisation_id": owner, "review_id": rev},
		{"campaign_id": "nope", "owner_user_id": owner, "review_id": rev},
		{"campaign_id": camp, "owner_user_id": owner},
	} {
		raw, _ = json.Marshal(bad)
		if _, err := TriggerFromEvent(EvCampaignReviewEscalated, raw); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("%v accepted: %v", bad, err)
		}
	}
	found := false
	for _, c := range (&Service{}).Consumers() {
		found = found || c.EventType == EvCampaignReviewEscalated && c.Name == ConsumerOpenCase
	}
	if !found {
		t.Fatal("campaigns.review_escalated is not subscribed")
	}
}
