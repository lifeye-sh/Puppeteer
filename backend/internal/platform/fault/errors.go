package fault

import "errors"

var Invalid = errors.New("invalid input")
var Conflict = errors.New("conflicting change")
var Denied = errors.New("permission denied")
var Proof = errors.New("invalid or reused proof")
var Locked = errors.New("account locked")
