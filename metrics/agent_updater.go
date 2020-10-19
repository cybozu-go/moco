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

func IncrementLogRotationCountMetrics() {
	logRotationCountMetrics.Inc()
}

func IncrementLogRotationFailureCountMetrics() {
	logRotationFailureCountMetrics.Inc()
}

func UpdateLogRotationDurationSecondsMetrics(durationSeconds float64) {
	logRotationDurationSecondsMetrics.Observe(durationSeconds)
}
