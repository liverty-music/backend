package entity

// Base45Encode exposes the RFC 9285 encoder so tests can build raw
// AdmissionCode payloads that Encode refuses to produce.
var Base45Encode = base45Encode
