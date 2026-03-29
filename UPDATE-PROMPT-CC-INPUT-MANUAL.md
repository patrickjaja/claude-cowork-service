Check @update-prompt.md — it describes how to update the reference materials (bin/ and vm-bundle/)
when a new Claude Desktop version is detected.

Run the extract scripts to check for updates:
```bash
./scripts/extract-cowork-svc.sh
./scripts/extract-vm-bundle.sh
```

If a new version was extracted:
1. Diff old vs new (cowork-svc.exe strings, vm-bundle-config.json, app.asar)
2. Run the protocol discovery prompts (Prompt 2 in update-prompt.md)
3. Verify Go code compatibility (Prompt 3 in update-prompt.md)
4. Update docs (COWORK_RPC_PROTOCOL.md, COWORK_VM_BUNDLE.md, COWORK_SVC_BINARY.md)
5. Commit the changes
