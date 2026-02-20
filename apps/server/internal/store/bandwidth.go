package store

// ── Bandwidth Usage ────────────────────────────────────────────────────

// AddBandwidth atomically increments egress bytes for a deployment in the given period.
func (s *Store) AddBandwidth(deploymentID, periodStart string, bytes int64) error {
	_, err := s.db.Exec(`
		INSERT INTO bandwidth_usage (deployment_id, period_start, bytes_out, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(deployment_id, period_start)
		DO UPDATE SET bytes_out = bytes_out + excluded.bytes_out, updated_at = CURRENT_TIMESTAMP`,
		deploymentID, periodStart, bytes,
	)
	return err
}

// GetBandwidth returns the current period egress bytes for a deployment.
func (s *Store) GetBandwidth(deploymentID, periodStart string) (int64, error) {
	var bytes int64
	err := s.db.QueryRow(
		"SELECT COALESCE(bytes_out, 0) FROM bandwidth_usage WHERE deployment_id = ? AND period_start = ?",
		deploymentID, periodStart,
	).Scan(&bytes)
	if err != nil {
		return 0, nil // no row = zero usage
	}
	return bytes, nil
}

// GetAllBandwidthForPeriod returns a map of deployment_id → bytes for every deployment
// that has usage in the given period.
func (s *Store) GetAllBandwidthForPeriod(periodStart string) (map[string]int64, error) {
	rows, err := s.db.Query(
		"SELECT deployment_id, bytes_out FROM bandwidth_usage WHERE period_start = ?",
		periodStart,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var id string
		var bytes int64
		rows.Scan(&id, &bytes)
		result[id] = bytes
	}
	return result, nil
}

// DeleteBandwidthBefore removes all bandwidth records with period_start before the given date.
func (s *Store) DeleteBandwidthBefore(date string) error {
	_, err := s.db.Exec("DELETE FROM bandwidth_usage WHERE period_start < ?", date)
	return err
}
