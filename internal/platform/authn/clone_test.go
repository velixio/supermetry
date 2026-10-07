package authn

import "testing"

func TestCloneSitesKeepsEmptyDistinctFromNil(t *testing.T) {
	if got := cloneSites(nil); got != nil {
		t.Fatalf("nil must stay nil (all sites), got %v", got)
	}
	if got := cloneSites([]int32{}); got == nil {
		t.Fatal("empty restriction became nil, which would grant all sites")
	}
}
