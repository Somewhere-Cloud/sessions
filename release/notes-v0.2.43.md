# Sessions 0.2.43

- Sessions that stayed paused after a restart can now be ended. `sessions kill`, or ending the session in the app, closes the paused record and removes its launch registration so it is never woken; the conversation stays resumable from history. Previously these sessions could not be closed or archived out of the list.

App updates preserve existing runners and histories.
