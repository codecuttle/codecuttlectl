package compact

// small_model.go provides aggressive compaction configuration for smaller/local
// models that suffer from "lost in the middle" attention decay when large tool
// results accumulate in the context window.
//
// For models like Gemma 4:31b (262k context), a single 70k-char read_file result
// can completely bury the user's original request. Standard compaction (preserve
// last 7 turns) is far too conservative — by the time it kicks in, the model has
// already lost the thread.

// SmallModelConfig returns a Config tuned for aggressive compaction suitable for
// smaller models with limited attention span. Key differences from DefaultConfig:
//   - PreserveRecentTurns: 1 (only the most recent tool result is kept verbatim)
//   - SummaryMaxLines: 6 (very compact summaries)
//   - MinResultSize: 500 (compact smaller results too)
//   - MaxContextPercent: 0.0 (always compact — don't wait for high usage)
func SmallModelConfig() Config {
	return Config{
		MaxContextPercent:   0.0, // Always compact stale results
		PreserveRecentTurns: 1,   // Only keep the most recent turn's results intact
		SummaryMaxLines:     6,   // Tighter summaries
		MinResultSize:       500, // Compact smaller results too
	}
}
