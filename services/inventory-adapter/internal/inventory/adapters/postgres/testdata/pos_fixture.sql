DROP TABLE IF EXISTS public."StockMovements" CASCADE;
DROP TABLE IF EXISTS public."CompositeItems" CASCADE;
DROP TABLE IF EXISTS public."Items" CASCADE;
DROP TABLE IF EXISTS public."Categories" CASCADE;
DROP TABLE IF EXISTS public."Users" CASCADE;

CREATE TABLE public."Users" (
    "Id" uuid PRIMARY KEY,
    "Name" text NOT NULL,
    "Username" varchar(64) NOT NULL UNIQUE,
    "Email" varchar(256),
    "PasswordHash" text,
    "Role" text NOT NULL,
    "IsActive" boolean NOT NULL,
    "CreatedAt" timestamptz NOT NULL,
    "UpdatedAt" timestamptz
);

CREATE TABLE public."Categories" (
    "Id" uuid PRIMARY KEY,
    "Name" text NOT NULL,
    "Description" text,
    "IsSystem" boolean NOT NULL DEFAULT false,
    "CreatedAt" timestamptz NOT NULL,
    "UpdatedAt" timestamptz
);

INSERT INTO public."Categories" (
    "Id", "Name", "Description", "CreatedAt"
) VALUES (
    'b4600000-0000-4000-8002-000000000001',
    'Daily essentials',
    'Disposable adapter test category',
    '2026-09-20T00:00:00Z'
);

CREATE TABLE public."Items" (
    "Id" uuid PRIMARY KEY,
	"Name" text NOT NULL DEFAULT '',
	"Description" text,
	"SellingPrice" numeric(18,2) NOT NULL DEFAULT 0,
    "Stock" integer NOT NULL,
    "IsActive" boolean NOT NULL,
    "TracksStock" boolean NOT NULL,
    "IsComposite" boolean NOT NULL,
	"CategoryId" uuid NOT NULL DEFAULT 'b4600000-0000-4000-8002-000000000001' REFERENCES public."Categories"("Id"),
	"CreatedAt" timestamptz NOT NULL DEFAULT now(),
    "UpdatedAt" timestamptz
);

CREATE TABLE public."CompositeItems" (
    "Id" uuid PRIMARY KEY,
    "ParentItemId" uuid NOT NULL REFERENCES public."Items"("Id"),
    "ComponentItemId" uuid NOT NULL REFERENCES public."Items"("Id"),
    "Quantity" numeric(18,3) NOT NULL CHECK ("Quantity" > 0),
    "CreatedAt" timestamptz NOT NULL,
    "UpdatedAt" timestamptz
);

CREATE TABLE public."StockMovements" (
    "Id" uuid PRIMARY KEY,
    "ItemId" uuid NOT NULL REFERENCES public."Items"("Id"),
    "Type" integer NOT NULL,
    "Quantity" integer NOT NULL,
    "CostPerUnit" numeric(18,2),
    "SupplierName" text,
    "Reason" integer,
    "Notes" text,
    "CreatedBy" uuid NOT NULL,
    "CreatedAt" timestamptz NOT NULL,
    "UpdatedAt" timestamptz
);

INSERT INTO public."Users" (
    "Id", "Name", "Username", "Role", "IsActive", "CreatedAt"
) VALUES (
    'b4600000-0000-4000-8000-000000000046',
    'B46 Online Ordering',
    'b46_online_ordering',
    'System',
    false,
    '2026-09-20T00:00:00Z'
);

INSERT INTO public."Items" (
    "Id", "Name", "Description", "SellingPrice", "Stock", "IsActive", "TracksStock", "IsComposite", "UpdatedAt"
) VALUES
    ('b4600000-0000-4000-8001-000000000001', 'Coke 1.5L', 'Chilled bottle', 82.00, 25, true, true, false, '2026-09-20T00:00:00Z'),
    ('b4600000-0000-4000-8001-000000000002', 'Tasty Bread', '600 g loaf', 68.00, 25, true, true, false, '2026-09-20T00:00:00Z'),
    ('b4600000-0000-4000-8001-000000000003', 'Fresh Milk 1L', 'Fresh dairy', 95.00, 25, true, true, false, '2026-09-20T00:00:00Z');

TRUNCATE b46_adapter.inventory_commit_receipts;
