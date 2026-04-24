package workspace

import "testing"

func TestStateCanTransitionTo(t *testing.T) {
	allStates := []State{StateCreated, StateStarting, StateRunning, StatePaused, StateStopped, StateRestored, StateRemoved}

	validTransitions := map[State][]State{
		StateCreated:  {StateStarting, StateRunning},
		StateStarting: {StateRunning, StateCreated},
		StateRunning:  {StatePaused, StateStopped},
		StatePaused:   {StateRunning, StateStopped},
		StateStopped:  {StateStarting, StateRunning, StateRestored},
		StateRestored: {StateRunning},
		StateRemoved:  {},
	}

	for _, from := range allStates {
		for _, to := range allStates {
			valid := false
			for _, allowed := range validTransitions[from] {
				if to == allowed {
					valid = true
					break
				}
			}
			if to == StateRemoved {
				valid = true
			}

			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				got := from.CanTransitionTo(to)
				if got != valid {
					t.Errorf("CanTransitionTo(%q) = %v, want %v", to, got, valid)
				}
			})
		}
	}
}

func TestStateIsTerminal(t *testing.T) {
	cases := []struct {
		state    State
		terminal bool
	}{
		{StateCreated, false},
		{StateStarting, false},
		{StateRunning, false},
		{StatePaused, false},
		{StateStopped, false},
		{StateRestored, false},
		{StateRemoved, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			if got := tc.state.IsTerminal(); got != tc.terminal {
				t.Errorf("IsTerminal() = %v, want %v", got, tc.terminal)
			}
		})
	}
}

func TestRemovedIsAlwaysReachable(t *testing.T) {
	states := []State{StateCreated, StateStarting, StateRunning, StatePaused, StateStopped, StateRestored}
	for _, s := range states {
		if !s.CanTransitionTo(StateRemoved) {
			t.Errorf("state %q should always be able to transition to Removed", s)
		}
	}
}
