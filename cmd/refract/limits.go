package main

import (
	"context"
	"database/sql"
	"errors"
)

// limits are what one account may hold and run. A zero field means no limit.
type limits struct {
	Projects      int
	StorageMB     int
	Turns         int
	PendingReview int
}

// plans are the hosted service's plans and their limits. Change the numbers
// here, nowhere else.
var plans = map[string]limits{
	"free": {Projects: 3, StorageMB: 200, Turns: 1, PendingReview: 2},
	"plus": {Projects: 100, StorageMB: 2048, Turns: 2, PendingReview: 5}, // own AI keys
	"ai":   {Projects: 100, StorageMB: 2048, Turns: 2, PendingReview: 5}, // Refract provides the AI (S1)
}

// limitsOf is the limits of an account on plan. An unknown plan gets the
// free limits: a typo must not lift them.
func limitsOf(plan string) limits {
	if l, ok := plans[plan]; ok {
		return l
	}
	return plans["free"]
}

// planOf reads an account's plan; an unknown account is on free.
func planOf(ctx context.Context, db *sql.DB, userID string) (string, error) {
	var plan string
	err := db.QueryRowContext(ctx, `SELECT plan FROM users WHERE id = ?`, userID).Scan(&plan)
	if errors.Is(err, sql.ErrNoRows) {
		return "free", nil
	}
	return plan, err
}
