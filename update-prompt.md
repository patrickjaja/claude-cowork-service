# Claude Cowork Service — Version Update Prompts

Reusable prompts for updating the cowork-service reference materials and verifying compatibility when a new Claude Desktop version drops.

## How to find the latest version

Check `https://downloads.claude.ai/releases/win32/x64/.latest` — returns JSON with `version` and `hash` fields.

The extract scripts auto-detect the latest version. Run them and they'll tell you if you're already up to date.

---

## Step 0: Clean Slate (Run First)

Before any version update, ensure a clean starting point:

> **Prepare for a clean cowork-service update.**
>
> 1. Check current versions:
>    ```bash
>    cat bin/.version vm-bundle/.version
>    ```
> 2. Save old files for diffing:
>    ```bash
>    # NOTE: cowork-svc.exe was removed from the installer in v1.6259.0
>    # Save app.asar and vm-bundle config for comparison instead
>    cp vm-bundle/vm-bundle-config.json /tmp/vm-bundle-config-old.json
>    cp bin/app.asar /tmp/app-asar-old.asar
>    ```
> 3. Verify clean git state:
>    ```bash
>    git status
>    ```
>
> Then proceed to **Prompt 1**.

---

## Prompt 1: Extract & Update Reference Materials

Copy-paste this into Claude Code when a new version is available:

> **New Claude Desktop version is available.** Please update the reference materials:
>
> 1. Run the extract scripts:
>    ```bash
>    ./scripts/extract-cowork-svc.sh
>    ./scripts/extract-vm-bundle.sh
>    ```
>    Both scripts are idempotent — they skip if already at the latest version.
>
> 2. Check what version was extracted:
>    ```bash
>    cat bin/.version vm-bundle/.version
>    ```
>
> 3. Compare app.asar for protocol changes (primary source of truth since v1.6259.0):
>    ```bash
>    # Extract old and new app.asar for comparison
>    npx @electron/asar extract /tmp/app-asar-old.asar /tmp/app-asar-old
>    npx @electron/asar extract bin/app.asar /tmp/app-asar-new
>    # Check for RPC method changes in the TypeScript VM client
>    diff <(rg -o 'method:"[^"]*"' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o 'method:"[^"]*"' /tmp/app-asar-new/app/.vite/build/index.js | sort -u)
>    ```
>
> 4. Compare VM bundle config:
>    ```bash
>    diff /tmp/vm-bundle-config-old.json vm-bundle/vm-bundle-config.json
>    ```
>    Note: SHA change = new VM images. Checksum changes = rebuilt files.
>
> 5. Update documentation:
>    - `COWORK_RPC_PROTOCOL.md` — if new methods or changed parameters found
>    - `COWORK_VM_BUNDLE.md` — update checksums, SHA, version history table
>    - `COWORK_SVC_BINARY.md` — update size, version history, note any new files
>
> 6. Commit with message: `Update bin/ and vm-bundle/ to v<VERSION>`
>    (Note: `.upstream-version` is updated automatically by the extract script)

---

## Prompt 2: Diff & Discover Protocol Changes

Copy-paste this to analyze what changed in the Electron app between two versions:

> **Compare Claude Desktop protocol changes between old and new version.**
>
> Prerequisites:
> - Old app.asar extracted: `/tmp/app-asar-old/` (or use `vm-bundle/app-asar-extracted/` from previous version)
> - New app.asar extracted: `vm-bundle/app-asar-extracted/`
>
> If not extracted yet:
> ```bash
> npx @electron/asar extract /tmp/app-asar-old.asar /tmp/app-asar-old
> ```
>
> Run these comparisons:
>
> 1. **Cowork-related code changes:**
>    ```bash
>    diff <(rg -o '.{0,60}cowork.{0,60}' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o '.{0,60}cowork.{0,60}' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u)
>    ```
>
> 2. **RPC method registrations (TypeScript VM client):**
>    ```bash
>    diff <(rg -o '.{0,40}(configure|createVM|startVM|stopVM|spawn|kill|writeStdin|subscribeEvents|mountPath|readFile|installSdk|isRunning|isGuestConnected|isProcessRunning|getDownloadStatus|setDebugLogging|addApprovedOauthToken).{0,40}' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o '.{0,40}(configure|createVM|startVM|stopVM|spawn|kill|writeStdin|subscribeEvents|mountPath|readFile|installSdk|isRunning|isGuestConnected|isProcessRunning|getDownloadStatus|setDebugLogging|addApprovedOauthToken).{0,40}' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u)
>    ```
>
> 3. **New RPC methods (look for method dispatch patterns):**
>    ```bash
>    rg 'method.*:.*"[a-z]' vm-bundle/app-asar-extracted/app/.vite/build/index.js | grep -v '//' | head -50
>    ```
>
> 4. **Spawn parameter changes:**
>    ```bash
>    diff <(rg -o '.{0,80}spawn.{0,80}' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o '.{0,80}spawn.{0,80}' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u)
>    ```
>
> 5. **Event type changes:**
>    ```bash
>    diff <(rg -o '.{0,40}(vmStarted|vmStopped|apiReachability|startupStep|stdout|stderr|exit).{0,40}' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o '.{0,40}(vmStarted|vmStopped|apiReachability|startupStep|stdout|stderr|exit).{0,40}' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u)
>    ```
>
> 6. **Session/dispatch changes:**
>    ```bash
>    diff <(rg -o '.{0,60}(dispatch|ditto|session_type|CLAUDE_CODE_TAGS|CLAUDE_CODE_BRIEF|disallowedTools|present_files).{0,60}' /tmp/app-asar-old/app/.vite/build/index.js | sort -u) \
>         <(rg -o '.{0,60}(dispatch|ditto|session_type|CLAUDE_CODE_TAGS|CLAUDE_CODE_BRIEF|disallowedTools|present_files).{0,60}' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u)
>    ```
>
> For each finding, classify as:
> - **Rename only** — minified variable name changed, no impact
> - **New feature** — new RPC method or parameter, may need Go implementation
> - **Changed behavior** — existing method changed, verify our implementation
> - **Removed** — method or parameter removed, clean up our code

---

## Prompt 3: Go Code Compatibility Audit

Run this on EVERY version update to verify our implementation still matches:

> **Audit Go code compatibility for Claude Desktop v<NEW_VERSION>.**
>
> 1. Check if all RPC methods in our handler match what Desktop expects:
>    ```bash
>    # Our methods:
>    rg 'case "' pipe/handlers.go
>    # Desktop's method calls (from app.asar):
>    rg -o 'method:"[^"]*"' vm-bundle/app-asar-extracted/app/.vite/build/index.js | sort -u
>    ```
>
> 2. Check spawn parameter handling:
>    - Are there new fields in the spawn request we're not parsing?
>    - Has the additionalMounts structure changed?
>    - Are there new environment variables we should handle?
>
> 3. Check event expectations:
>    - Does Desktop expect new event types?
>    - Has the event field format changed?
>    - Are there new startupStep values?
>
> 4. Check session types:
>    - New CLAUDE_CODE_TAGS values?
>    - Changes to brief flag behavior?
>    - New tools in --disallowedTools?
>
> 5. Test locally:
>    ```bash
>    make && ./cowork-svc-linux -debug
>    # Open Claude Desktop, start a Cowork session
>    # Watch debug output for any errors or unexpected params
>    ```
>
> 6. If changes needed:
>    - Update Go code
>    - Update COWORK_RPC_PROTOCOL.md
>    - Run `go vet ./...` and `go test ./...`
>    - Commit
>
> **Why:** Protocol mismatches between our implementation and Desktop's expectations
> cause silent failures — sessions hang, events are lost, tools don't work.
> Every upstream update must be verified.

---

## Common Patterns Across Versions

| What changes | How to detect | Impact |
|-------------|---------------|--------|
| New RPC method | Compare method dispatch in app.asar JS | Need new handler in pipe/handlers.go |
| New spawn parameter | Diff spawn-related JS | Update spawnParams struct |
| New event type | Search for event emission in JS | Add to process/events.go |
| VM bundle SHA change | Compare vm-bundle-config.json | Note in COWORK_VM_BUNDLE.md |
| New files in bin/ | Compare directory listings | Document in COWORK_SVC_BINARY.md |
| app.asar changes | Extract and diff index.js | Check for new RPC methods, spawn params, event types |
| Session type change | Search for CLAUDE_CODE_TAGS in JS | Update backend.go handling |
