# Changelog

## Unreleased

- Enabling "Start at Login" no longer launches a second instance on the spot. `login.Enable()` wrote the plist and then ran `launchctl bootstrap`, which honours `RunAtLoad` immediately — so ticking the tray toggle spawned a launchd-parented clone beside the app you ticked it in. Writing the plist is enough: launchd loads `~/Library/LaunchAgents` at login. The launchd `Label` also lost its stray `.plist` suffix (`com.zee.app`, was `com.zee.app.plist`)
