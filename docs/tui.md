---
title: Dashboard status
description: The persistent dashboard runtime is deferred.
icon: material/view-dashboard-outline
---

# Dashboard status

The persistent dashboard runtime is deferred during the CLI migration.
`jira tui` and `jira -i` report this explicitly without opening a dashboard
or resolving credentials. Use the issue and search commands instead.

Existing `[tui]` settings remain stored but are not executed. A replacement
dashboard and conversion of those settings require a separate implementation.
