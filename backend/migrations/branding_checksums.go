package migrations

// Only these four published SQL files received product-label-only edits.
// Accept the exact original/canonical pair, never arbitrary historical edits.
// Applied rows keep their original checksums as historical evidence. Migration
// 110 changes existing defaults; fresh installs use the canonical SQL directly.
var brandingRevisions = map[int][2]string{
	1:   {"c4f05d61786536a004b3f08da31b26813ddbc824a0d520b8836725b2f03e767b", "3cd945edefb0c4f780431fecd89456cd41bf8934f525e1efeea4af1c0b0c7e0f"},
	49:  {"2344e47aa1939b3d9ed756ab42037d0cb5b71106ec6faaa74bcfd4f94cc59466", "8293db6c7c48496f3ee35c9df0c962ded760ec6165f47c27bc044294843a0799"},
	54:  {"e0cbeb4dc956b85ab6abed226ad245379f2490ac97a8fc6345b2ece89b32b896", "3c905fcab29270276149e99062245342815d1c0447191f633379760341fa2ee4"},
	103: {"1f188e455ce65ff4a9ca422e80da21bb2dd93eb06f64a0b53885daedbb4109fb", "9afaed0b04691674e07f88021101cbb53f3fedf062ddf9a9a634c381f8d01aa0"},
}

var brandingLineEndingRevisions = map[int]string{
	1:   "3786b9f93377bdc019505032c64fd9b68e342404e700cc2e1d04d50799b25d87",
	49:  "ae45e083fd5a881655502974958568dcf46fa408cdd030d3574de376f617f0af",
	54:  "12d2390c1a0eaaa1fe982bb7a461fc5105a4b06c0554cace57a8679ebe9f85a0",
	103: "b201af4d90cb943af3393989019ef3d41376b84f3560f5aff98556b1023df647",
}

func knownBrandingRevision(version int, previous, canonical string) bool {
	pair, ok := brandingRevisions[version]
	return ok && (previous == pair[0] || previous == brandingLineEndingRevisions[version]) && canonical == pair[1]
}
