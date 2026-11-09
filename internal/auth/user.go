package auth

import (
	"context"
	"net/http"
	"strings"
)

// User is the signed-in Refract user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type userKey struct{}
type identityKey struct{}

// WithUser stores the request's user. Without one, access (internal/access)
// grants nothing: background work carries the user who started it.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// WithIdentity records who the auth layer says the request is from (an
// email); users.Middleware turns it into a User.
func WithIdentity(r *http.Request, email string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, strings.ToLower(strings.TrimSpace(email))))
}

// Identity is the email set by the auth middleware, "" if none.
func Identity(ctx context.Context) string {
	s, _ := ctx.Value(identityKey{}).(string)
	return s
}

// LocalIdentity is the single user of a deployment without authentication (REFRACT_AUTH=none).
const LocalIdentity = "local"

// Unauthenticated marks every request as the local user.
func Unauthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, WithIdentity(r, LocalIdentity))
	})
}
