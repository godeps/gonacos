// Package server lets external programs embed gonacos as an in-process
// Nacos v3-compatible service (HTTP + gRPC) instead of running the gonacos
// binary separately.
//
// Construct a [Server] with [New] and run it with [Server.Start]. Three
// usage modes are supported:
//
//  1. HTTP/gRPC in-process: call [Server.Start] and talk to it over
//     localhost (or let other SDK clients reach it).
//  2. Direct service call: [Server.Services] returns the service bundle
//     so callers can invoke config/naming/auth methods without a network hop.
//  3. Storage/snapshot access: [Server.Coordinator], [Server.Snapshot],
//     [Server.RedisClient] expose the persistence layer for backup/restore.
//
// Example:
//
//	srv, err := server.New(server.WithAddr(":8848"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	go func() { _ = srv.Start(context.Background()) }()
//	// Talk to it over http at http://localhost:8848, or call directly:
//	item, err := srv.Services().Config.Get(ctx, "public", "DEFAULT_GROUP", "app.yml")
//	// Backup the whole state on demand:
//	env, err := srv.Snapshot()
//	_ = os.WriteFile("backup.json", must(json.Marshal(env)), 0644)
package server
