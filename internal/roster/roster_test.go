package roster

import (
	"context"
	"errors"
	"testing"

	"github.com/juicetheforce/palworld-paladin/internal/palapi"
)

func pl(name string) palapi.Player { return palapi.Player{Name: name, UserID: "steam_" + name} }

func TestDifferEmitsJoinsAndLeaves(t *testing.T) {
	rosters := [][]palapi.Player{
		{pl("alice")},            // baseline: silent
		{pl("alice"), pl("bob")}, // bob joins
		{pl("bob")},              // alice leaves
	}
	i := 0
	var joins, leaves []string
	d := New(
		func(context.Context) ([]palapi.Player, error) { r := rosters[i]; i++; return r, nil },
		func(p palapi.Player) { joins = append(joins, p.Name) },
		func(p palapi.Player) { leaves = append(leaves, p.Name) },
	)
	for range rosters {
		if err := d.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(joins) != 1 || joins[0] != "bob" {
		t.Fatalf("baseline must be silent, then bob joins: %v", joins)
	}
	if len(leaves) != 1 || leaves[0] != "alice" {
		t.Fatalf("alice must leave: %v", leaves)
	}
}

func TestPollErrorsDoNotFabricateEvents(t *testing.T) {
	seq := 0
	var joins, leaves []string
	d := New(
		func(context.Context) ([]palapi.Player, error) {
			seq++
			switch seq {
			case 1:
				return []palapi.Player{pl("alice")}, nil // baseline
			case 2, 3:
				return nil, errors.New("rest down") // transient outage
			default:
				return []palapi.Player{pl("alice")}, nil // alice still there
			}
		},
		func(p palapi.Player) { joins = append(joins, p.Name) },
		func(p palapi.Player) { leaves = append(leaves, p.Name) },
	)
	for i := 0; i < 4; i++ {
		d.Poll(context.Background())
	}
	if len(joins) != 0 || len(leaves) != 0 {
		t.Fatalf("a poll outage must not fabricate events: joins=%v leaves=%v", joins, leaves)
	}
}
