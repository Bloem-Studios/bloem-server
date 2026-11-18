-- +goose Up
-- Storage scans add a run's directories, roots, listed entries and cursors to
-- these tables as the run goes. The default autoanalyze threshold is 10% of a
-- table, which on a large location is not reached during a run: the planner
-- keeps treating the run in progress as empty and picks plans that loop over
-- its rows, until a page takes longer than the scan lease. Fixed thresholds
-- keep the statistics up to date while a run is in progress.
ALTER TABLE public.bloem_storage_scan_directories SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);
ALTER TABLE public.bloem_storage_scan_directory_roots SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);
ALTER TABLE public.bloem_storage_scan_directory_entries SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);
ALTER TABLE public.bloem_storage_scan_cursors SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);
ALTER TABLE public.bloem_storage_location_edges SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);
ALTER TABLE public.bloem_storage_location_entries SET (autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 10000, autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50000);

-- +goose Down
ALTER TABLE public.bloem_storage_scan_directories RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
ALTER TABLE public.bloem_storage_scan_directory_roots RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
ALTER TABLE public.bloem_storage_scan_directory_entries RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
ALTER TABLE public.bloem_storage_scan_cursors RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
ALTER TABLE public.bloem_storage_location_edges RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
ALTER TABLE public.bloem_storage_location_entries RESET (autovacuum_analyze_scale_factor, autovacuum_analyze_threshold, autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold);
