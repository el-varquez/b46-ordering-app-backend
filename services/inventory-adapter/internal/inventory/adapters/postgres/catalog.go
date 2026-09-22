package postgres

import (
	"context"
	"fmt"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

func (store *Store) Catalog(ctx context.Context, query domain.CatalogQuery) (domain.CatalogPage, error) {
	afterID := uuid.Nil
	if query.AfterID != uuid.Nil {
		afterID = query.AfterID
	}
	rows, err := store.pool.Query(ctx, `
		SELECT i."Id", i."Name", COALESCE(i."Description", ''),
		       ROUND(i."SellingPrice" * 100)::bigint,
		       c."Id", c."Name",
		       CASE
		           WHEN NOT i."IsActive" THEN false
		           WHEN NOT i."TracksStock" THEN true
		           WHEN NOT i."IsComposite" THEN i."Stock" > 0
		           ELSE EXISTS (
		               SELECT 1 FROM public."CompositeItems" any_component
		               WHERE any_component."ParentItemId" = i."Id"
		           ) AND NOT EXISTS (
		               SELECT 1
		               FROM public."CompositeItems" ci
		               LEFT JOIN public."Items" component ON component."Id" = ci."ComponentItemId"
		               WHERE ci."ParentItemId" = i."Id"
		                 AND (
		                     component."Id" IS NULL OR NOT component."IsActive" OR
		                     (component."TracksStock" AND component."Stock" < CEIL(ci."Quantity")::integer)
		                 )
		           )
		       END AS available,
		       COALESCE(i."UpdatedAt", i."CreatedAt")
		FROM public."Items" i
		JOIN public."Categories" c ON c."Id" = i."CategoryId"
		WHERE i."Id" > $1
		ORDER BY i."Id"
		LIMIT $2
	`, afterID.String(), query.Limit+1)
	if err != nil {
		return domain.CatalogPage{}, fmt.Errorf("query POS catalog: %w", err)
	}
	defer rows.Close()

	products := make([]domain.CatalogProduct, 0, query.Limit+1)
	for rows.Next() {
		var product domain.CatalogProduct
		if err := rows.Scan(
			&product.ProductID,
			&product.Name,
			&product.Description,
			&product.PriceCentavos,
			&product.CategoryID,
			&product.CategoryName,
			&product.Available,
			&product.SourceUpdatedAt,
		); err != nil {
			return domain.CatalogPage{}, fmt.Errorf("scan POS catalog product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return domain.CatalogPage{}, fmt.Errorf("iterate POS catalog: %w", err)
	}

	page := domain.CatalogPage{}
	if len(products) > query.Limit {
		page.Products = products[:query.Limit]
		page.NextAfterID = page.Products[len(page.Products)-1].ProductID
	} else {
		page.Products = products
	}
	return page, nil
}
