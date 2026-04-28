package main

import (
	"encoding/json"
	"fmt"
	"time"
)

type rmsLearningSeedRow struct {
	FormatKey    string
	Bins         int
	MaxRMS       float64
	Silence      []uint64
	Music        []uint64
	SilenceTotal int64
	MusicTotal   int64
	DerivedEnter float64
	DerivedExit  float64
}

// Seed captured from a stable real-world Pi setup; used as a starter baseline
// for empty installs that want sensible autonomous learning defaults.
var defaultRMSLearningSeedRows = []rmsLearningSeedRow{
	{
		FormatKey: "cd",
		Bins:      80,
		MaxRMS:    0.25,
		Silence:   []uint64{0, 0, 19676, 74, 12, 6, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Music:     []uint64{0, 0, 0, 703, 1025, 796, 804, 769, 860, 924, 1103, 1320, 1679, 1930, 2239, 2547, 2681, 2803, 2773, 3049, 3185, 3231, 3400, 3359, 3367, 3209, 3067, 2992, 2836, 2558, 2537, 2298, 2247, 2052, 1899, 1740, 1761, 1583, 1421, 1312, 1230, 1015, 897, 808, 782, 716, 623, 621, 513, 499, 426, 390, 396, 332, 304, 303, 254, 298, 241, 230, 210, 190, 216, 186, 175, 152, 134, 141, 134, 118, 109, 96, 92, 81, 73, 57, 53, 49, 44, 367},
		SilenceTotal: 19771,
		MusicTotal:   91614,
		DerivedEnter: 0.01656249910593033,
		DerivedExit:  0.028437498956918716,
	},
	{
		FormatKey: "physical",
		Bins:      80,
		MaxRMS:    0.25,
		Silence:   []uint64{0, 2848, 261975, 39147, 39, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Music:     []uint64{0, 0, 0, 21, 73, 88, 73, 74, 81, 80, 74, 80, 51, 30, 55, 35, 59, 32, 34, 34, 31, 27, 30, 30, 30, 27, 41, 21, 21, 42, 18, 21, 21, 17, 20, 19, 13, 28, 29, 20, 24, 19, 24, 23, 27, 26, 33, 29, 23, 28, 33, 21, 22, 21, 13, 18, 16, 16, 18, 19, 20, 11, 18, 9, 12, 11, 13, 8, 8, 6, 11, 7, 4, 4, 5, 4, 2, 5, 4, 27},
		SilenceTotal: 304019,
		MusicTotal:   2122,
		DerivedEnter: 0.013562500476837158,
		DerivedExit:  0.017124999314546585,
	},
	{
		FormatKey: "vinyl",
		Bins:      80,
		MaxRMS:    0.25,
		Silence:   []uint64{0, 32, 5449, 356, 151, 120, 76, 39, 12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Music:     []uint64{0, 0, 0, 831, 893, 1072, 1518, 1636, 1546, 1574, 1681, 1641, 1728, 1672, 1585, 1676, 1705, 1677, 1677, 1721, 1745, 1748, 1815, 1904, 1846, 1983, 2034, 2065, 2049, 2052, 2118, 2084, 2042, 2096, 2164, 2155, 2081, 2082, 2121, 1999, 2004, 1866, 1886, 1823, 1727, 1670, 1643, 1531, 1468, 1352, 1316, 1250, 1264, 1147, 1104, 1048, 978, 951, 877, 895, 847, 828, 719, 678, 674, 630, 638, 624, 583, 550, 527, 492, 490, 447, 442, 469, 393, 363, 391, 14583},
		SilenceTotal: 6235,
		MusicTotal:   119184,
		DerivedEnter: 0.01793750189244747,
		DerivedExit:  0.027437502518296242,
	},
}

func (l *LibraryDB) importDefaultRMSLearningBaseline(overwrite bool) (int, bool, error) {
	if l == nil || l.db == nil {
		return 0, false, fmt.Errorf("library database unavailable")
	}

	var existing int
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM rms_learning`).Scan(&existing); err != nil {
		return 0, false, fmt.Errorf("count rms_learning rows: %w", err)
	}
	if existing > 0 && !overwrite {
		return 0, true, nil
	}

	if overwrite {
		if _, err := l.db.Exec(`DELETE FROM rms_learning`); err != nil {
			return 0, false, fmt.Errorf("clear existing rms_learning rows: %w", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	imported := 0
	for _, row := range defaultRMSLearningSeedRows {
		silJSON, err := json.Marshal(row.Silence)
		if err != nil {
			return imported, false, fmt.Errorf("marshal silence histogram (%s): %w", row.FormatKey, err)
		}
		musJSON, err := json.Marshal(row.Music)
		if err != nil {
			return imported, false, fmt.Errorf("marshal music histogram (%s): %w", row.FormatKey, err)
		}
		if _, err := l.db.Exec(`
			INSERT INTO rms_learning (
				format_key, updated_at, bins, max_rms, silence_counts, music_counts,
				silence_total, music_total, derived_enter, derived_exit
			) VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(format_key) DO UPDATE SET
				updated_at=excluded.updated_at,
				bins=excluded.bins,
				max_rms=excluded.max_rms,
				silence_counts=excluded.silence_counts,
				music_counts=excluded.music_counts,
				silence_total=excluded.silence_total,
				music_total=excluded.music_total,
				derived_enter=excluded.derived_enter,
				derived_exit=excluded.derived_exit`,
			row.FormatKey, now, row.Bins, row.MaxRMS,
			string(silJSON), string(musJSON),
			row.SilenceTotal, row.MusicTotal, row.DerivedEnter, row.DerivedExit,
		); err != nil {
			return imported, false, fmt.Errorf("upsert rms baseline row (%s): %w", row.FormatKey, err)
		}
		imported++
	}
	return imported, false, nil
}
