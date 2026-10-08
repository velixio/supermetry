CREATE TABLE heatmap_cells (
    site_id integer NOT NULL,
    day date NOT NULL,
    path_id integer NOT NULL,
    device smallint NOT NULL CHECK (device BETWEEN 0 AND 2),
    gx smallint NOT NULL CHECK (gx BETWEEN 0 AND 99),
    gy integer NOT NULL CHECK (gy BETWEEN 0 AND 5000),
    clicks integer NOT NULL CHECK (clicks > 0),
    PRIMARY KEY (site_id, day, path_id, device, gx, gy)
) PARTITION BY RANGE (day);
