package playback

// applyForceTranscodeV3 applies PlannerInputV3.ForceTranscode (admin replan
// override, S-5a) to the resolved quality policy.
func applyForceTranscodeV3(input PlannerInputV3, quality QualityResultV3) QualityResultV3 {
	if input.ForceTranscode && !quality.RequiresTranscode {
		// Pinned by an admin replan: keep the resolved rung (original quality
		// or the capped rung) but route it through the transcoder. ExplicitRung
		// keeps the automatic "reduction unavailable" fallback below from
		// undoing the pin.
		quality.RequiresTranscode = true
		quality.ExplicitRung = true
		quality.Reason = "admin_transcode_forced"
	}
	return quality
}
