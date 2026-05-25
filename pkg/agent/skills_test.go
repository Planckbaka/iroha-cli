package agent

import (
	"testing"
)

func newTestSkillManager() *SkillManager {
	return &SkillManager{byID: make(map[string]*SkillManifest)}
}

func TestSkillManagerMatchTriggers(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{
		ID:       "tdd-workflow",
		Name:     "TDD Workflow",
		Triggers: []string{"tdd", "test first"},
		Type:     SkillTypeModelInvoked,
	})
	sm.skills = append(sm.skills, &SkillManifest{
		ID:       "autopilot",
		Name:     "Autopilot Mode",
		Triggers: []string{"autopilot", "auto mode"},
		Type:     SkillTypeModelInvoked,
	})
	sm.byID["tdd-workflow"] = sm.skills[0]
	sm.byID["autopilot"] = sm.skills[1]
	sm.mu.Unlock()

	// Test matching
	matched := sm.MatchTriggers("let's use tdd for this feature")
	if len(matched) != 1 || matched[0].ID != "tdd-workflow" {
		t.Errorf("expected tdd-workflow match, got %v", matched)
	}

	// Test no match
	matched = sm.MatchTriggers("just a normal request")
	if len(matched) != 0 {
		t.Errorf("expected no matches, got %d", len(matched))
	}

	// Test case insensitive
	matched = sm.MatchTriggers("I want AUTOPILOT mode")
	if len(matched) != 1 || matched[0].ID != "autopilot" {
		t.Errorf("expected autopilot match (case insensitive), got %v", matched)
	}
}

func TestSkillManagerMatchTriggersSkipsNonModelInvoked(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{
		ID:       "always-skill",
		Name:     "Always Active",
		Triggers: []string{"always-trigger"},
		Type:     SkillTypeAlways,
	})
	sm.skills = append(sm.skills, &SkillManifest{
		ID:       "user-skill",
		Name:     "User Invoked",
		Triggers: []string{"user-trigger"},
		Type:     SkillTypeUserInvoked,
	})
	sm.byID["always-skill"] = sm.skills[0]
	sm.byID["user-skill"] = sm.skills[1]
	sm.mu.Unlock()

	// Neither always nor user_invoked skills should match triggers
	matched := sm.MatchTriggers("always-trigger user-trigger")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for non-model_invoked skills, got %d", len(matched))
	}
}

func TestSkillManagerGetByID(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{ID: "test-skill", Name: "Test"})
	sm.byID["test-skill"] = sm.skills[0]
	sm.mu.Unlock()

	s := sm.GetSkillByID("test-skill")
	if s == nil || s.Name != "Test" {
		t.Error("expected to find test-skill")
	}

	s = sm.GetSkillByID("nonexistent")
	if s != nil {
		t.Error("expected nil for nonexistent skill")
	}
}

func TestSkillManagerGetAlwaysSkills(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{ID: "always-1", Name: "Always 1", Type: SkillTypeAlways})
	sm.skills = append(sm.skills, &SkillManifest{ID: "model-1", Name: "Model 1", Type: SkillTypeModelInvoked})
	sm.skills = append(sm.skills, &SkillManifest{ID: "always-2", Name: "Always 2", Type: SkillTypeAlways})
	sm.byID["always-1"] = sm.skills[0]
	sm.byID["model-1"] = sm.skills[1]
	sm.byID["always-2"] = sm.skills[2]
	sm.mu.Unlock()

	always := sm.GetAlwaysSkills()
	if len(always) != 2 {
		t.Fatalf("expected 2 always skills, got %d", len(always))
	}
	for _, s := range always {
		if s.Type != SkillTypeAlways {
			t.Errorf("expected SkillTypeAlways, got %q", s.Type)
		}
	}
}

func TestSkillManagerGetUserInvokedSkills(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{ID: "user-1", Name: "User 1", Type: SkillTypeUserInvoked})
	sm.skills = append(sm.skills, &SkillManifest{ID: "model-1", Name: "Model 1", Type: SkillTypeModelInvoked})
	sm.byID["user-1"] = sm.skills[0]
	sm.byID["model-1"] = sm.skills[1]
	sm.mu.Unlock()

	userInvoked := sm.GetUserInvokedSkills()
	if len(userInvoked) != 1 {
		t.Fatalf("expected 1 user_invoked skill, got %d", len(userInvoked))
	}
	if userInvoked[0].ID != "user-1" {
		t.Errorf("expected 'user-1', got %q", userInvoked[0].ID)
	}
}

func TestSkillManagerAllSkills(t *testing.T) {
	sm := newTestSkillManager()

	sm.mu.Lock()
	sm.skills = append(sm.skills, &SkillManifest{ID: "s1", Name: "Skill 1"})
	sm.skills = append(sm.skills, &SkillManifest{ID: "s2", Name: "Skill 2"})
	sm.byID["s1"] = sm.skills[0]
	sm.byID["s2"] = sm.skills[1]
	sm.mu.Unlock()

	all := sm.AllSkills()
	if len(all) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(all))
	}

	// Verify it returns a copy (modifying returned slice doesn't affect internal state)
	all[0] = nil
	inner := sm.AllSkills()
	if inner[0] == nil {
		t.Error("AllSkills() should return a copy, not internal slice")
	}
}
