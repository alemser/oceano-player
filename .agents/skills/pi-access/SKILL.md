---
name: pi-access
description: SSH access to Raspberry Pi at 192.168.178.99 for debugging, log analysis, and running commands. Use when user asks to check logs, debug services, or run commands on the Pi.
---

# Raspberry Pi SSH Access

Use SSH to connect to the Raspberry Pi for debugging, testing, and running commands.

## Connection

```
Host: oceano
IP: 192.168.178.99
User: alemser
Auth: SSH key (passwordless)
```

## Quick Commands

```bash
# Connect and run command
ssh alemser@192.168.178.99 "command"

# Check service logs
ssh alemser@192.168.178.99 "journalctl -u oceano-web.service -n 50 --no-pager"

# Restart service
ssh alemser@192.168.178.99 "sudo systemctl restart oceano-web.service"

# Check all oceano services
ssh alemser@192.168.178.99 "systemctl status oceano-*"
```

## Common Tasks

- Restart services after config changes
- Check logs for errors
- Verify installed version
- Test recognition flow
- Debug audio capture issues

## Notes

- Services run as systemd units: `oceano-source-detector`, `oceano-state-manager`, `oceano-web`
- Config at `/etc/oceano/config.json`
- State at `/tmp/oceano-state.json`
- Library DB at `/var/lib/oceano/library.db`