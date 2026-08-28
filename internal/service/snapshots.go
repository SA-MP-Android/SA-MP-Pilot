package service

import (
	"slices"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
)

func cloneSnapshot(source domain.Snapshot) domain.Snapshot {
	result := source
	result.Chat = nil
	result.Players = slices.Clone(source.Players)
	result.NearbyPlayers = slices.Clone(source.NearbyPlayers)
	result.Vehicles = slices.Clone(source.Vehicles)
	result.Objects = slices.Clone(source.Objects)
	result.TextDraws = slices.Clone(source.TextDraws)
	result.Dialogs = slices.Clone(source.Dialogs)
	result.Commands = slices.Clone(source.Commands)
	if source.ActiveDialog != nil {
		dialog := *source.ActiveDialog
		result.ActiveDialog = &dialog
	}
	return result
}
