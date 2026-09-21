package entity

import "errors"

// ErrOrderNotFound is returned when a lookup found no such order, as opposed to failing to ask.
//
// It lives here rather than in the database package so the HTTP handlers can tell the two apart
// without importing that package: a webhook for an order this instance does not carry is an
// ordinary outcome — the other shop's order, or one never synced — while a database that cannot
// answer is a fault, and they do not belong at the same log level.
var ErrOrderNotFound = errors.New("order not found")
