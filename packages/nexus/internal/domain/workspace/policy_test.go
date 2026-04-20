package workspace

import "testing"

func TestPolicyValidate(t *testing.T) {
	cases := []struct {
		name    string
		policy  Policy
		wantErr bool
	}{
		{"zero values", Policy{}, false},
		{"valid auto stop", Policy{AutoStop: true, AutoStopDelay: 60, MaxLifetimeSec: 3600}, false},
		{"negative auto stop delay", Policy{AutoStopDelay: -1}, true},
		{"negative max lifetime", Policy{MaxLifetimeSec: -1}, true},
		{"both negative", Policy{AutoStopDelay: -5, MaxLifetimeSec: -10}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestErrorSentinelsDistinct(t *testing.T) {
	errs := []error{ErrNotFound, ErrAlreadyExists, ErrInvalidTransition, ErrInvalidState}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Errorf("errors[%d] and errors[%d] are the same value", i, j)
			}
		}
	}
}
