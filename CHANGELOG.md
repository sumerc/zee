# Changelog

## Unreleased

- The recording overlay says "Recording" sooner: the contents fade started 100 ms into the 220 ms shape expansion, so the panel opened as a blank black notch and the label only reached full opacity at 260 ms. Now 40 ms in over 140 ms — just enough head start for the shape to outgrow the label
- Enabling "Start at Login" no longer launches a second instance on the spot. `login.Enable()` wrote the plist and then ran `launchctl bootstrap`, which honours `RunAtLoad` immediately — so ticking the tray toggle spawned a launchd-parented clone beside the app you ticked it in. Writing the plist is enough: launchd loads `~/Library/LaunchAgents` at login. The launchd `Label` also lost its stray `.plist` suffix (`com.zee.app`, was `com.zee.app.plist`)
