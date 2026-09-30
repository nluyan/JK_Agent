package config

import "testing"

func TestJitterSecondsUsesDefaultsWhenUnset(t *testing.T) {
	s := &Settings{}
	if got := s.StartupJitterSeconds(); got != DefaultStartupJitter {
		t.Fatalf("未配置时 StartupJitterSeconds() = %d, 期望 %d", got, DefaultStartupJitter)
	}
	if got := s.UpdateJitterSeconds(); got != DefaultUpdateJitter {
		t.Fatalf("未配置时 UpdateJitterSeconds() = %d, 期望 %d", got, DefaultUpdateJitter)
	}
}

func TestJitterSecondsNegativeDisablesJitter(t *testing.T) {
	s := &Settings{StartupJitter: -1, UpdateJitter: -30}
	if got := s.StartupJitterSeconds(); got != 0 {
		t.Fatalf("配置为负数时 StartupJitterSeconds() = %d, 期望 0", got)
	}
	if got := s.UpdateJitterSeconds(); got != 0 {
		t.Fatalf("配置为负数时 UpdateJitterSeconds() = %d, 期望 0", got)
	}
}

func TestJitterSecondsUsesExplicitValue(t *testing.T) {
	s := &Settings{StartupJitter: 30, UpdateJitter: 600}
	if got := s.StartupJitterSeconds(); got != 30 {
		t.Fatalf("StartupJitterSeconds() = %d, 期望 30", got)
	}
	if got := s.UpdateJitterSeconds(); got != 600 {
		t.Fatalf("UpdateJitterSeconds() = %d, 期望 600", got)
	}
}
