package implement

import "strings"

// agyRate is public per-million-token API pricing for one model family
// available through Antigravity CLI (agy). agy's stream-json never reports
// a dollar cost (confirmed against real run logs and Google's headless
// docs) - only token counts. This table estimates what the same tokens
// would cost against the model's billed API, since that's the only cost
// signal agy gives us short of Google adding native cost reporting.
//
// CacheReadPerM is deliberately not the same as InPerM: cache reads are
// billed far cheaper than fresh input (e.g. gemini-3.8-flash: $0.075 vs
// $0.75 per 1M). Cache *storage* (a per-hour holding cost, independent of
// any one call's token counts) has no rate here - agy's per-run usage
// gives us token counts, not cache TTL/duration, so it isn't modelable
// from a single run and is intentionally omitted rather than guessed.
type agyRate struct {
	InPerM, OutPerM, CacheReadPerM float64
}

// agyModelRates covers models confirmed live in Antigravity as of 2026-09.
// Flash family rates are introductory pricing through 2026-12-31 (standard
// pricing of $1.50/$7.50 input/output, $0.15 cache-read applies from
// 2027-01-01 - update this table then). Pro uses the <=200k-context
// standard tier; long-context and batch/priority tiers are not modeled.
// Models without a verified public rate (Claude Sonnet/Opus 4.6,
// gpt-oss-120b) are intentionally omitted rather than guessed -
// EstimateAgyCost reports "not ok" for them.
var agyModelRates = map[string]agyRate{
	"gemini-3.6-flash": {InPerM: 0.75, OutPerM: 3.75, CacheReadPerM: 0.075},
	"gemini-3.7-flash": {InPerM: 0.75, OutPerM: 3.75, CacheReadPerM: 0.075},
	"gemini-3.8-flash": {InPerM: 0.75, OutPerM: 3.75, CacheReadPerM: 0.075},
	"gemini-3.1-pro":   {InPerM: 2.00, OutPerM: 12.00, CacheReadPerM: 0.20},
}

// agyEffortSuffixes are reasoning-effort suffixes agy appends to a model
// id (e.g. "gemini-3.8-flash-medium"). Pricing is per model family, not
// per effort variant, so these are stripped before rate lookup.
var agyEffortSuffixes = []string{"-high", "-medium", "-low"}

// EstimateAgyCost estimates the USD cost of an agy run from its model id
// and token counts, using agyModelRates. inTok is the total input token
// count as agy reports it (fresh input + cache reads folded together, per
// agyUsage); cacheReadTok is the cache-read subset of inTok, billed
// separately at the model's cheaper cache-read rate. Reports ok=false for
// models with no verified public rate rather than guessing.
func EstimateAgyCost(model string, inTok, cacheReadTok, outTok int64) (float64, bool) {
	family := model
	for _, suffix := range agyEffortSuffixes {
		if stripped, ok := strings.CutSuffix(family, suffix); ok {
			family = stripped
			break
		}
	}
	rate, ok := agyModelRates[family]
	if !ok {
		return 0, false
	}
	freshIn := inTok - cacheReadTok
	cost := float64(freshIn)/1_000_000*rate.InPerM +
		float64(cacheReadTok)/1_000_000*rate.CacheReadPerM +
		float64(outTok)/1_000_000*rate.OutPerM
	return cost, true
}
