package models

// unknownLabel is the canonical string form of every enum's zero / invalid
// value (SourceUnknown, AssetClassUnknown, FreshnessUnknown). Extracted to a
// single constant so goconst stops flagging the duplication and so a future
// rename only has to touch one place.
const unknownLabel = "unknown"
