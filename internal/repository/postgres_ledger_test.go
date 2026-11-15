package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nurtw/escrow/internal/domain"
)

type corridorPolicyFixture struct {
	id          string
	origin      string
	destination string
	model       string
	active      bool
}

type corridorPolicyConnector struct {
	policies []corridorPolicyFixture
	err      error
}

func (c corridorPolicyConnector) Connect(context.Context) (driver.Conn, error) {
	return corridorPolicyConn{connector: c}, nil
}

func (c corridorPolicyConnector) Driver() driver.Driver {
	return corridorPolicyDriver{}
}

type corridorPolicyDriver struct{}

func (corridorPolicyDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use corridorPolicyConnector")
}

type corridorPolicyConn struct {
	connector corridorPolicyConnector
}

func (corridorPolicyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (corridorPolicyConn) Close() error {
	return nil
}

func (corridorPolicyConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c corridorPolicyConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "origin_park_id = $1 AND destination_park_id = $2 AND is_active = TRUE") {
		return nil, fmt.Errorf("unexpected corridor policy query: %s", query)
	}
	if c.connector.err != nil {
		return nil, c.connector.err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("got %d query args, want 2", len(args))
	}

	origin, originOK := args[0].Value.(string)
	destination, destinationOK := args[1].Value.(string)
	if !originOK || !destinationOK {
		return nil, errors.New("corridor policy query args must be strings")
	}

	for _, policy := range c.connector.policies {
		if policy.origin == origin && policy.destination == destination && policy.active {
			return &corridorPolicyRows{values: []driver.Value{
				policy.id,
				"Test corridor",
				policy.model,
				int64(500000),
				int64(2000),
				int64(50000),
				int64(830),
				int64(0),
				int64(500),
				int64(0),
			}}, nil
		}
	}
	return &corridorPolicyRows{}, nil
}

type corridorPolicyRows struct {
	values []driver.Value
	done   bool
}

func (corridorPolicyRows) Columns() []string {
	return []string{
		"id", "corridor_name", "model", "threshold_kobo",
		"platform_basis_points", "platform_floor_kobo",
		"agent_basis_points", "agent_fixed_kobo",
		"union_basis_points", "union_fixed_kobo",
	}
}

func (r *corridorPolicyRows) Close() error {
	return nil
}

func (r *corridorPolicyRows) Next(dest []driver.Value) error {
	if r.done || len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}

func newCorridorPolicyTestRepository(t *testing.T, connector corridorPolicyConnector) *EscrowRepository {
	t.Helper()

	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return NewEscrowRepository(db)
}

func TestGetCorridorPolicy(t *testing.T) {
	databaseErr := errors.New("query failed")
	tests := []struct {
		name        string
		policies    []corridorPolicyFixture
		err         error
		wantID      string
		wantModel   domain.PricingModel
		wantErr     error
		wantWrapped error
	}{
		{
			name: "exact active route match",
			policies: []corridorPolicyFixture{{
				id:          "KAD_ABJ",
				origin:      "KAD-01",
				destination: "ABJ-01",
				model:       string(domain.PricingModelProportional),
				active:      true,
			}},
			wantID:    "KAD_ABJ",
			wantModel: domain.PricingModelProportional,
		},
		{
			name: "no exact route match",
			policies: []corridorPolicyFixture{{
				id:          "OTHER",
				origin:      "KAD-01",
				destination: "KAN-01",
				model:       string(domain.PricingModelProportional),
				active:      true,
			}},
			wantErr: ErrNoCorridorPolicy,
		},
		{
			name: "inactive exact route does not match",
			policies: []corridorPolicyFixture{{
				id:          "INACTIVE",
				origin:      "KAD-01",
				destination: "ABJ-01",
				model:       string(domain.PricingModelProportional),
				active:      false,
			}},
			wantErr: ErrNoCorridorPolicy,
		},
		{
			name:        "database errors remain distinct from no policy",
			err:         databaseErr,
			wantWrapped: databaseErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCorridorPolicyTestRepository(t, corridorPolicyConnector{
				policies: tt.policies,
				err:      tt.err,
			})

			policy, err := repo.GetCorridorPolicy(context.Background(), "KAD-01", "ABJ-01")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("GetCorridorPolicy() error = %v, want %v", err, tt.wantErr)
				}
				if policy != nil {
					t.Fatalf("GetCorridorPolicy() policy = %#v, want nil", policy)
				}
				return
			}
			if tt.wantWrapped != nil {
				if !errors.Is(err, tt.wantWrapped) {
					t.Fatalf("GetCorridorPolicy() error = %v, want wrapped %v", err, tt.wantWrapped)
				}
				if errors.Is(err, ErrNoCorridorPolicy) {
					t.Fatalf("GetCorridorPolicy() error = %v, must not be classified as no policy", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetCorridorPolicy() error = %v, want nil", err)
			}
			if policy == nil || policy.ID != tt.wantID || policy.Model != tt.wantModel {
				t.Fatalf("GetCorridorPolicy() = %#v, want ID %q and model %q", policy, tt.wantID, tt.wantModel)
			}
		})
	}
}
