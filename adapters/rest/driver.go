package restadapter

import "github.com/loykin/dbstore"

// DriverBuilder is the REST driver contract expected by Adapter.RegisterDriver.
// REST drivers are application-owned so each backend can decide how DSNs,
// headers, auth, and transport settings map to a Client.
type DriverBuilder = dbstore.DriverBuilder[*Client]
