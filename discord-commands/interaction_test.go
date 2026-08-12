package main

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestInteractionAcknowledgementVoiceJoinIsImmediate(t *testing.T) {
	ack := interactionAcknowledgement(discordgo.ApplicationCommandInteractionData{
		Name: "voice",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Name: "join",
			Type: discordgo.ApplicationCommandOptionSubCommand,
		}},
	})
	if ack.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("voice join acknowledgement type = %v, want immediate message", ack.Type)
	}
	if ack.Data == nil || ack.Data.Content == "" {
		t.Fatal("voice join acknowledgement has no visible content")
	}
	if ack.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("voice join acknowledgement flags = %v, want ephemeral", ack.Data.Flags)
	}
}

func TestInteractionAcknowledgementOrdinaryCommandRemainsDeferred(t *testing.T) {
	ack := interactionAcknowledgement(discordgo.ApplicationCommandInteractionData{Name: "inventory"})
	if ack.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("ordinary acknowledgement type = %v, want deferred response", ack.Type)
	}
}

func TestShouldCompleteAfterAckFailureOnlyForVoiceLeave(t *testing.T) {
	tests := []struct {
		name string
		data discordgo.ApplicationCommandInteractionData
		want bool
	}{
		{
			name: "voice leave",
			data: discordgo.ApplicationCommandInteractionData{
				Name: "voice",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{{
					Name: "leave",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				}},
			},
			want: true,
		},
		{
			name: "voice join",
			data: discordgo.ApplicationCommandInteractionData{
				Name: "voice",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{{
					Name: "join",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				}},
			},
		},
		{name: "ordinary command", data: discordgo.ApplicationCommandInteractionData{Name: "inventory"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCompleteAfterAckFailure(tt.data); got != tt.want {
				t.Fatalf("shouldCompleteAfterAckFailure() = %t, want %t", got, tt.want)
			}
		})
	}
}
