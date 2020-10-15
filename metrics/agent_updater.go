package metrics

func IncrementCloneCountMetrics() {
	cloneCountMetrics.Inc()
}

func IncrementCloneFailureCountMetrics() {
	cloneFailureCountMetrics.Inc()
}

func UpdateCloneDurationSecondsMetrics(durationSeconds float64) {
	cloneDurationSecondsMetrics.Observe(durationSeconds)
}
