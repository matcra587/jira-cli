// Package dashboard owns the issue home screen and its feature workflows.
// Widgets come from Primer; Jira operations and public configuration remain
// capabilities bound by the ordinary CLI owners.
//
// Model state changes on the Tea update loop. Input routes to one focus owner,
// and task results pass generation admission before reaching a feature handler.
// Runtime replacement also checks the originating model, so older clients cannot
// update a replacement workspace. Rendering reuses accepted issue projections.
package dashboard
