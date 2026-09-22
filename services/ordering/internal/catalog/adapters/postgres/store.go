package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (store *Store) ReplaceSnapshot(ctx context.Context, products []domain.Product, now time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin catalog snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var revision int64
	if err := tx.QueryRow(ctx, `
		UPDATE catalog_sync_state
		SET current_revision = current_revision + 1, updated_at = $1
		WHERE singleton = true
		RETURNING current_revision
	`, now).Scan(&revision); err != nil {
		return fmt.Errorf("reserve catalog revision: %w", err)
	}
	for _, product := range products {
		if err := product.Validate(); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO catalog_products (
				product_id, name, description, price_centavos, category_id,
				category_name, image_url, available, source_updated_at,
				catalog_revision, created_at
			) VALUES (
				$1,$2,$3,$4,$5,$6,
				(SELECT image_url FROM catalog_products WHERE product_id = $1 ORDER BY catalog_revision DESC LIMIT 1),
				$7,$8,$9,$10
			)
		`, product.ID, product.Name, product.Description, product.PriceCentavos,
			product.CategoryID, product.CategoryName, product.Available,
			product.SourceUpdatedAt, revision, now)
		if err != nil {
			return fmt.Errorf("insert catalog product %s: %w", product.ID, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog_products (
			product_id, name, description, price_centavos, category_id,
			category_name, image_url, available, source_updated_at,
			catalog_revision, created_at
		)
		SELECT previous.product_id, previous.name, previous.description,
		       previous.price_centavos, previous.category_id, previous.category_name,
		       previous.image_url, false, previous.source_updated_at, $1::bigint, $2::timestamptz
		FROM catalog_products previous
		WHERE previous.catalog_revision = $1::bigint - 1
		  AND NOT EXISTS (
		      SELECT 1 FROM catalog_products current
		      WHERE current.catalog_revision = $1::bigint
		        AND current.product_id = previous.product_id
		  )
	`, revision, now); err != nil {
		return fmt.Errorf("retain missing catalog products as unavailable: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM catalog_products WHERE catalog_revision < $1::bigint`, revision-1); err != nil {
		return fmt.Errorf("prune catalog snapshots: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE catalog_sync_state SET last_completed_at = $1, updated_at = $1 WHERE singleton = true
	`, now); err != nil {
		return fmt.Errorf("complete catalog snapshot: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) Products(ctx context.Context, limit int, afterID string, snapshot int64) (domain.Page, error) {
	if snapshot == 0 {
		if err := store.pool.QueryRow(ctx, `SELECT current_revision FROM catalog_sync_state WHERE singleton = true`).Scan(&snapshot); err != nil {
			return domain.Page{}, fmt.Errorf("read catalog revision: %w", err)
		}
	}
	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_products WHERE catalog_revision = $1)`, snapshot).Scan(&exists); err != nil {
		return domain.Page{}, fmt.Errorf("check catalog snapshot: %w", err)
	}
	if !exists {
		return domain.Page{}, domain.ErrSnapshotExpired
	}
	rows, err := store.pool.Query(ctx, `
		SELECT product_id, name, description, price_centavos, category_id,
		       category_name, COALESCE(image_url, ''), available,
		       source_updated_at, catalog_revision
		FROM catalog_products
		WHERE catalog_revision = $1
		  AND ($2 = '' OR product_id > $2::uuid)
		ORDER BY product_id
		LIMIT $3
	`, snapshot, afterID, limit+1)
	if err != nil {
		return domain.Page{}, fmt.Errorf("list catalog products: %w", err)
	}
	defer rows.Close()
	products, err := scanProducts(rows)
	if err != nil {
		return domain.Page{}, err
	}
	page := domain.Page{SnapshotRevision: snapshot}
	if len(products) > limit {
		page.Products = products[:limit]
		page.NextAfterID = page.Products[len(page.Products)-1].ID
	} else {
		page.Products = products
	}
	return page, nil
}

func (store *Store) Resolve(ctx context.Context, productIDs []string) ([]domain.Product, error) {
	if len(productIDs) == 0 {
		return nil, nil
	}
	rows, err := store.pool.Query(ctx, `
		SELECT product_id, name, description, price_centavos, category_id,
		       category_name, COALESCE(image_url, ''), available,
		       source_updated_at, catalog_revision
		FROM catalog_products
		WHERE catalog_revision = (SELECT current_revision FROM catalog_sync_state WHERE singleton = true)
		  AND product_id = ANY(ARRAY(SELECT value::uuid FROM unnest($1::text[]) AS value))
		ORDER BY product_id
	`, productIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve catalog products: %w", err)
	}
	defer rows.Close()
	return scanProducts(rows)
}

type rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanProducts(result rows) ([]domain.Product, error) {
	products := make([]domain.Product, 0)
	for result.Next() {
		var product domain.Product
		if err := result.Scan(&product.ID, &product.Name, &product.Description,
			&product.PriceCentavos, &product.CategoryID, &product.CategoryName,
			&product.ImageURL, &product.Available, &product.SourceUpdatedAt,
			&product.Revision); err != nil {
			return nil, fmt.Errorf("scan catalog product: %w", err)
		}
		products = append(products, product)
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog products: %w", err)
	}
	return products, nil
}
