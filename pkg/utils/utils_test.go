package utils

import (
	"testing"
)

func TestCleanFolderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Heaven's Lost Property", "Heaven's Lost Property"},
		{"Nisekoi: False Love", "Nisekoi False Love"},
		{"KissXsis", "KissXsis"},
		{"Mayo Chiki!", "Mayo Chiki!"}, // ! is usually allowed in Windows/Linux, but : is not
		{"Monster Musume: Everyday Life with Monster Girls", "Monster Musume Everyday Life with Monster Girls"},
		{"IS: Infinite Stratos", "IS Infinite Stratos"},
		{"Date a Live", "Date a Live"},
		{"Samurai Girls", "Samurai Girls"},
		{"Sekirei", "Sekirei"},
		{"Freezing", "Freezing"},
		{"Aesthetica of a Rogue Hero", "Aesthetica of a Rogue Hero"},
		{"The Quintessential Quintuplets", "The Quintessential Quintuplets"},
		{"Trinity Seven", "Trinity Seven"},
		{"We Never Learn", "We Never Learn"},
		{"Maken-Ki!", "Maken-Ki!"},
		{"My Gift Lvl 9999 Unlimited Gacha: Backstabbed in a Backwater Dungeon, I’m Out for Revenge!", "My Gift Lvl 9999 Unlimited Gacha Backstabbed in a Backwater Dungeon, I’m Out for Revenge!"},
		{"Let's Play", "Let's Play"},
		{"Bofuri: I Don't Want to Get Hurt, so I'll Max Out My Defense", "Bofuri I Don't Want to Get Hurt, so I'll Max Out My Defense"},
		{"Shikimori's Not Just a Cutie", "Shikimori's Not Just a Cutie"},
		{"My One-Hit Kill Sister", "My One-Hit Kill Sister"},
		{"Hensuki: Are You Willing to Fall in Love with a Pervert, as Long as She's a Cutie?", "Hensuki Are You Willing to Fall in Love with a Pervert, as Long as She's a Cutie"},
		{"Don’t Toy With Me, Miss Nagatoro", "Don’t Toy With Me, Miss Nagatoro"},
		{"Mysterious Girlfriend X", "Mysterious Girlfriend X"},
		{"Shomin Sample", "Shomin Sample"},
		{"Chained Soldier", "Chained Soldier"},
		{"Platinum End", "Platinum End"},
		{"Re:ZERO - Starting Life in Another World", "ReZERO - Starting Life in Another World"},
		{"How I Attended an All-Guy's Mixer", "How I Attended an All-Guy's Mixer"},
		{"The Unaware Atelier Meister", "The Unaware Atelier Meister"},
		{"Akame ga Kill!", "Akame ga Kill!"},
		{"That Time I Got Reincarnated as a Slime", "That Time I Got Reincarnated as a Slime"},
		{"Loner Life in Another World", "Loner Life in Another World"},
		{"Tales of Wedding Rings", "Tales of Wedding Rings"},
		{"Mushoku Tensei: Jobless Reincarnation", "Mushoku Tensei Jobless Reincarnation"},
		{"You and I Are Polar Opposites", "You and I Are Polar Opposites"},
		{"SPY x FAMILY", "SPY x FAMILY"},
		{"More Than a Married Couple, But Not Lovers", "More Than a Married Couple, But Not Lovers"},
		{"My Dress-Up Darling", "My Dress-Up Darling"},
		{"Welcome to Demon School! Iruma-kun", "Welcome to Demon School! Iruma-kun"},
		{"Danmachi - Is It Wrong to Try to Pick Up Girls in a Dungeon", "Danmachi - Is It Wrong to Try to Pick Up Girls in a Dungeon"},
		{"And You Thought There Is Never a Girl Online?", "And You Thought There Is Never a Girl Online"},
		{"Frieren: Beyond Journey's End", "Frieren Beyond Journey's End"},
		{"Yosuga No Sora: In Solitude Where We Are Least Alone", "Yosuga No Sora In Solitude Where We Are Least Alone"},
		{"Farming Life in Another World", "Farming Life in Another World"},
		{"Panty & Stocking with Garterbelt", "Panty & Stocking with Garterbelt"},
		{"Date a Bullet", "Date a Bullet"},
		{"Fire Force", "Fire Force"},
		{"In Another World With My Smartphone", "In Another World With My Smartphone"},
		{"Gushing over Magical Girls", "Gushing over Magical Girls"},
		{"Haganai: I Don’t Have Many Friends", "Haganai I Don’t Have Many Friends"},
		{"Iwakakeru: Sport Climbing Girls", "Iwakakeru Sport Climbing Girls"},
		{"Interspecies Reviewers", "Interspecies Reviewers"},
		{"Spice and Wolf: merchant meets the wise wolf", "Spice and Wolf merchant meets the wise wolf"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanFolderName(tt.input)
			if got != tt.expected {
				t.Errorf("\nInput:    %s\nExpected: %s\nGot:      %s", tt.input, tt.expected, got)
			}
		})
	}
}
