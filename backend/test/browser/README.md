# R16 isolated browser acceptance

`TestR16IsolatedBrowserFixture` is opt-in and refuses any database except the
disposable `r16-postgres-fixture/nt_r16_test` service. It creates a random schema,
synthetic accounts, and a real local TLS LDAP fixture. Normal application tests
skip the long-running fixture. The fixture uses the existing captcha-free test
server; it does not change production configuration or production authentication.

Run it with `NT_R16_BROWSER_FIXTURE=1`, `NT_R16_BROWSER_OUTPUT=/src/.local/r16-browser`
and the disposable test DSN. Docker must publish **127.0.0.1:44087:44087** only.
Do not publish this fixture to a LAN. Credentials and LDAP connection material
are written to `.local/r16-browser/fixture.json` and removed at shutdown.

Start a separate Vite process in `acceptance` mode with `VITE_BACKEND_MODE=new`,
`DEV_API_TARGET=http://127.0.0.1:44087`, host `127.0.0.1` and port `5185`.
Use **http://r16-ui.localhost:5185/**, never the business `backend.localhost`
hostname: browser cookies must remain isolated. The visible platform name is
`R16 隔离验收`.

The fixture expires after 30 minutes. Creating `.local/r16-browser/stop` shuts
it down gracefully and drops its random database schema. Stop the separate Vite
process too. Passing this synthetic browser acceptance does not claim production
administrator-page acceptance or real DBeaver/Windows-device acceptance.
