package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userRepoFixture is what each backend setup hands to the compliance suite:
// a working UserRepository plus enough executor access to probe transaction
// behavior directly (raw INSERT for the rollback case). ph renders a bind
// placeholder for position n ("?" for SQLite/MySQL, "$n" for PostgreSQL).
type userRepoFixture struct {
	repo   UserRepository
	exec   *Executor[*sqlx.DB]
	source string
	ph     func(n int) string
}

// runUserRepoComplianceSuite runs one set of behavioral assertions against
// any UserRepository implementation. Every backend (SQLite, PostgreSQL, ...)
// must pass the same suite, so drift between implementations is caught here
// instead of divergently in backend-specific tests.
func runUserRepoComplianceSuite(t *testing.T, setup func(t *testing.T) userRepoFixture) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_and_FindByID", func(t *testing.T) {
		f := setup(t)
		require.NoError(t, f.repo.Create(ctx, "Alice"))

		users, err := f.repo.FindAll(ctx)
		require.NoError(t, err)
		require.Len(t, users, 1)

		u, err := f.repo.FindByID(ctx, users[0].ID)
		require.NoError(t, err)
		assert.Equal(t, "Alice", u.Name)
	})

	t.Run("FindAll", func(t *testing.T) {
		f := setup(t)
		require.NoError(t, f.repo.Create(ctx, "Alice"))
		require.NoError(t, f.repo.Create(ctx, "Bob"))

		users, err := f.repo.FindAll(ctx)
		require.NoError(t, err)
		assert.Len(t, users, 2)
		assert.Equal(t, "Alice", users[0].Name)
		assert.Equal(t, "Bob", users[1].Name)
	})

	t.Run("FindByID_NotFound", func(t *testing.T) {
		f := setup(t)
		_, err := f.repo.FindByID(ctx, 999)
		assert.Error(t, err)
	})

	t.Run("CreateBatch_Commit", func(t *testing.T) {
		f := setup(t)
		require.NoError(t, f.repo.CreateBatch(ctx, []string{"Alice", "Bob", "Carol"}))

		users, err := f.repo.FindAll(ctx)
		require.NoError(t, err)
		assert.Len(t, users, 3)
	})

	t.Run("CreateBatch_Rollback", func(t *testing.T) {
		f := setup(t)
		err := RunTx(f.exec, ctx, f.source, func(ctx context.Context, tx *sqlx.Tx) error {
			_, _ = tx.ExecContext(ctx, `INSERT INTO users (name) VALUES (`+f.ph(1)+`)`, "ShouldRollback")
			return errors.New("intentional")
		})
		assert.Error(t, err)

		users, err := f.repo.FindAll(ctx)
		require.NoError(t, err)
		assert.Len(t, users, 0)
	})
}
