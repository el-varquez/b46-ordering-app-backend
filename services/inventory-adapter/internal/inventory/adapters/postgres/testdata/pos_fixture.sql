DROP TABLE IF EXISTS public."StockMovements" CASCADE;
DROP TABLE IF EXISTS public."CompositeItems" CASCADE;
DROP TABLE IF EXISTS public."Items" CASCADE;
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

CREATE TABLE public."Items" (
    "Id" uuid PRIMARY KEY,
    "Stock" integer NOT NULL,
    "IsActive" boolean NOT NULL,
    "TracksStock" boolean NOT NULL,
    "IsComposite" boolean NOT NULL,
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
    "Id", "Stock", "IsActive", "TracksStock", "IsComposite", "UpdatedAt"
) VALUES
    ('b4600000-0000-4000-8001-000000000001', 25, true, true, false, '2026-09-20T00:00:00Z'),
    ('b4600000-0000-4000-8001-000000000002', 25, true, true, false, '2026-09-20T00:00:00Z'),
    ('b4600000-0000-4000-8001-000000000003', 25, true, true, false, '2026-09-20T00:00:00Z');

TRUNCATE b46_adapter.inventory_commit_receipts;
