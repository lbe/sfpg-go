# Phase 2 Test Merge Map (WP-54)

Companion to `docs/phase2-test-ownership.md`. WP-54 is **complete**; this document records the merge plan and executed results.

**Starting point (after WP-53):** 48 root `internal/server/*_test.go` files.

**Exit (after WP-54):** 19 survivors; root `CreateApp` mentions 64 (from 110); coverage gate 0/0 uncovered functions.

**Arithmetic:** 48 − 33 deleted + 4 new survivor files = **19** files. Net reduction = 29 files (48 − 19).

---

## Survivor list (19 files)

### Unit (no build tag) — 7 survivors

1. **`app_test.go`** (canonical App construction / delegation survivor)
   - Tag: (unit)
   - Sources to delete: `handler_manager_test.go`, `app_startup_mock_test.go`, `etag_cli_test.go`
   - Tests to port:
     - `TestHandlerManager_Build_*` → `app_test.go`
     - `TestParseConfigUITemplates_EmptyFS` → `app_test.go`
     - `TestApp_Run_*` (from `app_startup_mock_test.go`) → `app_test.go`
     - `TestStartCacheBatchLoad_*` → `app_test.go`
     - `TestApp_IncrementETag*`, `TestApp_InitForIncrementETag*` (from `etag_cli_test.go`) → `app_test.go`
   - Helpers to port: any private helpers from source files.
   - CreateApp strategy: keep existing `app_test.go` calls; migrated tests that only need partial App state should reuse `App` partial literals where possible.

2. **`server_test.go`** (canonical root unit-test survivor)
   - Tag: (unit)
   - Sources to delete: `bench_test.go`, `server_handlers_test.go`
   - Tests to port:
     - Benchmarks from `bench_test.go` → `server_test.go`
     - Any unit tests from `server_handlers_test.go` → `server_test.go`
     - Path-traversal tests already planned for `server_test.go`
   - Helpers to port: `removeImagesDirPrefix` helper from `bench_test.go` if not already shared.

3. **`helpers_test.go`** (canonical shared-test-infrastructure survivor)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: keep `CreateApp`, `AppOption`, `With*`, `setenvForTest` here. Do not duplicate in other unit survivors.

4. **`auth_service_test.go`** (deferred WP-53 auth-facade survivor)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: keep `SessionAuthFacade` tests here. Does not use `CreateApp`.

5. **`infrastructure_service_test.go`** (manager unit survivor)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: keep infrastructure unit tests.

6. **`runtime_manager_test.go`** (manager unit survivor)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: keep runtime manager unit tests.

7. **`subsystem_manager_test.go`** (manager unit survivor)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: keep subsystem manager unit tests.

8. **`metrics_adapters_test.go`** (deferred WP-53 adapter survivor; WP-19 rewritten)
   - Tag: (unit)
   - Sources to delete: none
   - Notes: after WP-19, tests `cachelite.HTTPCacheMiddleware` and `files.ProcessingStats` interface satisfaction (adapters deleted).

### Integration (`//go:build integration`) — 6 survivors

9. **`app_lifecycle_integration_test.go`**
   - Tag: `//go:build integration`
   - Sources to delete:
     - `app_integration_test.go`
     - `app_profile_integration_test.go`
     - `app_serve_test.go`
     - `app_startup_integration_test.go`
     - `app_startup_test.go`
     - `restart_cli_test.go`
     - `server_restart_test.go` (ownership-map follow-up)
   - Tests to port:
     - All `TestApp_*` / `TestImageDirectoryIntegration_*` from `app_integration_test.go`
     - `TestApp_LogProfileLocation` from `app_profile_integration_test.go`
     - `TestApp_Serve_*` from `app_serve_test.go`
     - `TestRun_Integration_*`, `TestStartupLogging_*`, `TestLogStartupConfigSummary_*`, `TestStartup_OrderingConstraint` from `app_startup_integration_test.go`
     - `TestApp_Run_*` from `app_startup_test.go`
     - `TestProcessRestart_RequestsRestartAndExecs` from `restart_cli_test.go`
   - CreateApp strategy: consolidate similar setup into package-local helpers; replace full `CreateApp()` with partial `App` literals where only specific fields are exercised.

10. **`config_lifecycle_integration_test.go`**
    - Tag: `//go:build integration`
    - Sources to delete:
      - `config_bootstrap_integration_test.go`
      - `config_pool_precedence_test.go`
      - `config_restart_persistence_integration_test.go`
      - `config_startup_restart_regression_test.go`
      - `server_config_test.go`
      - `config_last_known_good_integration_test.go` (ownership-map follow-up)
    - Tests to port:
      - `TestLoadConfig_CompleteStateAfterFreshDatabase`
      - `TestPoolPrecedence_*` / `TestDBPoolPrecedence_*`
      - `TestConfigRestartPersistence_*` / `TestConfigPersistence_*`
      - `TestConfigStartupRestart_*` / `TestStartupWithDBConfig_*`, `TestRestartWithModifiedDBConfig_*`
      - `TestServerConfig_*` / `TestSetConfigDefaults_*`, `TestParseConfigUITemplates_Coverage`, `TestLoadConfig_Coverage`, `TestApplyConfig_Coverage`
      - `TestLastKnownGood_*`
    - CreateApp strategy: share `createAppForConfigTest` helper; use partial `App` literals where only config fields are exercised.

11. **`infrastructure_service_integration_test.go`**
    - Tag: `//go:build integration`
    - Sources to delete: none
    - Notes: keep existing integration tests.

12. **`runtime_manager_integration_test.go`**
    - Tag: `//go:build integration`
    - Sources to delete: none
    - Notes: keep existing integration tests.

13. **`subsystem_manager_integration_test.go`**
    - Tag: `//go:build integration`
    - Sources to delete: none
    - Notes: keep existing integration tests.

14. **`server_integration_test.go`**
    - Tag: `//go:build integration`
    - Sources to delete:
      - `middleware_wiring_integration_test.go`
      - `security_integration_test.go`
      - `server_session_integration_test.go`
      - `etag_cache_invalidation_integration_test.go`
    - Tests to port:
      - `TestGetRouter_*`
      - `TestCrossOriginProtection_*Full`, `TestAuthenticationRequired`, `TestSessionManagement_*`
      - `TestGetSessionOptionsConfig`, `TestGetSessionOptions_WithLoadedConfig`, `TestSessionExpiry_SessionMaxAge`
      - `TestETagIncrement_InvalidatesHTTPCache`, `TestApplyConfig_InvalidatesCacheWhenETagChanges`, `TestApplyConfig_DoesNotInvalidateWhenETagUnchanged`
    - CreateApp strategy: use a shared `createAppWithRouter` helper for tests that only need `app.getRouter`.

### `integration || e2e` — 3 survivors

15. **`config_precedence_integration_test.go`** (keeps `integration || e2e`)
    - Tag: `//go:build integration || e2e`
    - Sources to delete:
      - `config_import_integration_test.go`
      - `config_precedence_integration_test.go` (self-merge / rename kept)
    - Tests to port:
      - `TestConfigImport_*`
      - `TestConfigPrecedence_*`, `TestCLI_*`
    - CreateApp strategy: consolidate CLI/env/database precedence setup; reuse partial `App` literals where possible.

16. **`logging_integration_test.go`** (keeps `integration || e2e`)
    - Tag: `//go:build integration || e2e`
    - Sources to delete:
      - `logging_alignment_integration_test.go`
      - `logging_api_integration_test.go`
      - `logging_startup_integration_test.go`
    - Tests to port:
      - `TestFileAlignment_*`
      - `TestConfigAPI_*`
      - `TestStartupLogging_*`
    - CreateApp strategy: share a `createAppWithLogDir` helper for logging tests.

17. **`helpers_integration_test.go`** (keeps `integration || e2e`)
    - Tag: `//go:build integration || e2e`
    - Sources to delete: none
    - Notes: keep shared auth helpers (`addAuthToRequest`, `extractCSRFTokenFromConfig`, `extractCSRFTokenFromLogin`, `loginAsAdmin`). Used by integration and e2e survivors.

### E2E (`//go:build e2e`) — 2 survivors

18. **`server_e2e_test.go`**
    - Tag: `//go:build e2e`
    - Sources to delete:
      - `cache_batch_load_e2e_test.go`
      - `config_e2e_test.go`
      - `config_restart_integration_test.go`
      - `config_save_and_restart_flow_test.go`
      - `handlers_cache_headers_e2e_test.go`
      - `integration_test.go`
      - `server_cache_e2e_test.go`
    - Tests to port:
      - `TestE2E_CacheBatchLoad_*`, `TestE2E_CacheBatchLoad_CLI_*`
      - `TestConfigE2E_*`, `TestRouter_*`
      - `TestConfigRestart_*`
      - `TestConfigSaveAndRestart_*`
      - `TestE2E_GalleryByID_CacheHeaders`, `TestE2E_ImageByID_CacheHeaders`, `TestE2E_LightboxByID_CacheHeaders`
      - `TestE2E_*` gallery/admin flows from `integration_test.go`
      - `TestE2E_CacheAndCompression_*`
    - CreateApp strategy: share a single `createAppForE2E` helper; where tests only exercise the router, use a partial `App` literal.

19. **`admin_credentials_integration_test.go`**
    - Tag: `//go:build e2e`
    - Sources to delete: none
    - Notes: keep `TestAdminCredentials_E2E_UpdateFlow` here, or merge into `server_e2e_test.go` if preferred. Kept separate to maintain small survivor count.

---

## Executed deletion ledger (WP-54)

Git recorded **33 deletions** and **4 additions** (`tmp/wp-54-moves.txt`):

| Category               | Count | Deleted sources                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ---------------------- | ----: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unit                   |     5 | `handler_manager_test.go`, `app_startup_mock_test.go`, `etag_cli_test.go`, `bench_test.go`, `server_handlers_test.go`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| Integration            |    17 | `app_integration_test.go`, `app_profile_integration_test.go`, `app_serve_test.go`, `app_startup_integration_test.go`, `app_startup_test.go`, `restart_cli_test.go`, `server_restart_test.go`, `config_bootstrap_integration_test.go`, `config_pool_precedence_test.go`, `config_restart_persistence_integration_test.go`, `config_startup_restart_regression_test.go`, `server_config_test.go`, `config_last_known_good_integration_test.go`, `middleware_wiring_integration_test.go`, `security_integration_test.go`, `server_session_integration_test.go`, `etag_cache_invalidation_integration_test.go` |
| `integration \|\| e2e` |     4 | `config_import_integration_test.go`, `logging_alignment_integration_test.go`, `logging_api_integration_test.go`, `logging_startup_integration_test.go`                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| E2E                    |     7 | `cache_batch_load_e2e_test.go`, `config_e2e_test.go`, `config_restart_integration_test.go`, `config_save_and_restart_flow_test.go`, `handlers_cache_headers_e2e_test.go`, `integration_test.go`, `server_cache_e2e_test.go`                                                                                                                                                                                                                                                                                                                                                                                |

**New survivor files:** `app_lifecycle_integration_test.go`, `config_lifecycle_integration_test.go`, `logging_integration_test.go`, `server_e2e_test.go`.

**Extended survivors:** `app_test.go`, `server_test.go`, `server_integration_test.go`, `config_precedence_integration_test.go`.

**Unchanged survivors (11):** `helpers_test.go`, `helpers_integration_test.go`, `auth_service_test.go`, `metrics_adapters_test.go`, `infrastructure_service_test.go`, `infrastructure_service_integration_test.go`, `runtime_manager_test.go`, `runtime_manager_integration_test.go`, `subsystem_manager_test.go`, `subsystem_manager_integration_test.go`, `admin_credentials_integration_test.go`.

Net: 48 − 33 + 4 = **19 survivors** (29 fewer files than before WP-54).

---

## CreateApp reduction (achieved)

Root `CreateApp` mentions: **110 → 64** (target ≤70). Repository-wide: **64** (target ≤110).

Tactics per survivor:

- `app_lifecycle_integration_test.go`: source files contain ~28 CreateApp mentions (app_integration 22, app_serve 4, app_startup_integration 2, restart_cli 2). Consolidate repeated full-app setups into a helper that returns a preconfigured `*App`. Several `app_integration` tests may only need `app.setDB` + pools — replace with partial `App` literals.
- `config_lifecycle_integration_test.go`: source files contain ~15 CreateApp mentions (config_restart 2, config_restart_persistence 1, config_save 1, config_startup 1, server_config 4, config_import would move to precedence). Share a `createAppWithConfig` helper and use partial literals for config-only tests.
- `server_integration_test.go`: source files contain ~20 CreateApp mentions (etag 6, middleware 6, security 5, server_session 3). Use a shared `createAppWithRouter` helper; router-only tests can use a minimal `App` literal with `getRouter` wired.
- `server_e2e_test.go`: source files contain ~20 CreateApp mentions (integration_test 6, server_cache 5, handlers_cache_headers 4, config_e2e 3, cache_batch_load 1, config_restart 2 from precedence move?). Consolidate into one `createAppForE2E` helper and partial literals where possible.
- Keep unit survivors' CreateApp usage minimal; `helpers_test.go` has 4 mentions that are part of the helper API and should not be reduced.

Result: **64** root mentions — WP-55 not required.

---

## Build-tag compatibility notes

- Never merge files with incompatible tags.
- Unit files stay unit; integration files stay `integration`.
- `integration || e2e` files can only merge with other `integration || e2e` files, or be kept separate.
- E2E files merge only into E2E survivors.
- Helpers used across tag boundaries live in files tagged `integration || e2e` (e.g., `helpers_integration_test.go`).
