package scanner

import "testing"

func TestValidateCommitFilter(t *testing.T) {
	tests := []struct {
		name    string
		filter  CommitFilter
		wantErr bool
	}{
		{"empty", CommitFilter{}, false},
		{"valid dates", CommitFilter{Since: "2024-01-01", Until: "2025-06-30"}, false},
		{"valid refs", CommitFilter{FromCommit: "abc123", ToCommit: "v1.2.3"}, false},
		{"valid refs with slashes", CommitFilter{FromCommit: "feature/x-1"}, false},
		{"range operator in ref", CommitFilter{FromCommit: "abc..def"}, true},
		{"leading dash", CommitFilter{FromCommit: "-U"}, true},
		{"space in ref", CommitFilter{ToCommit: "master x"}, true},
		{"option injection", CommitFilter{ToCommit: "--exec=evil"}, true},
		{"bad date format", CommitFilter{Since: "01.02.2024"}, true},
		{"date with garbage", CommitFilter{Until: "2024-01-01; rm -rf"}, true},
		{"empty string ok", CommitFilter{Since: "", Until: ""}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCommitFilter(tt.filter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCommitFilter(%+v) error = %v, wantErr %v", tt.filter, err, tt.wantErr)
			}
		})
	}
}
