package templates

import "fmt"

// mongoAuth signs mongosh in as root, reading the password from the env
// inside the script, so it's never an argument. It goes before a script
// that needs it.
const mongoAuth = `db.getSiblingDB("admin").auth(process.env.MONGO_INITDB_ROOT_USERNAME, process.env.MONGO_INITDB_ROOT_PASSWORD); `

// mongoAppUser is the script the image's entrypoint runs once, on an empty
// volume, signed in as root: it makes the user apps connect as, who owns
// the app's database and nothing else.
const mongoAppUser = `const name = process.env.MONGO_INITDB_DATABASE; ` +
	`db.getSiblingDB(name).createUser({user: process.env.MONGO_USER, pwd: process.env.MONGO_PASSWORD, roles: [{role: "dbOwner", db: name}]})`

// mongoSetup writes mongoAppUser where the entrypoint finds it, and turns
// off mongosh's log, which would otherwise leave a file in the container
// for every healthcheck and every check Echowa runs. It raises the open
// files limit to MongoDB's recommended 64000, within Docker's hard limit:
// WiredTiger holds a file open for each collection and index, and Docker
// starts tasks at 1024. Where the hard limit is lower, mongod starts with
// what there is.
const mongoSetup = `ulimit -n 64000 2>/dev/null; ` +
	`printf 'mongosh:\n  disableLogging: true\n  enableTelemetry: false\n' > /etc/mongosh.conf; ` +
	`printf '%s\n' '` + mongoAppUser + `' > /docker-entrypoint-initdb.d/echowa-app-user.js; `

// mongosh runs a script signed in as root.
func mongosh(script string) []string {
	return []string{"mongosh", "--quiet", "--norc", "--eval", mongoAuth + script}
}

// mongoHealth prints DatabaseStatus's keys. The cache is WiredTiger's:
// a page requested and not read in from disk was a hit. Commits are every
// operation, as Redis's are every command; aggregate is counted in
// command.
const mongoHealth = `const s = db.adminCommand({serverStatus: 1});
const cache = s.wiredTiger.cache, ops = s.opcounters;
const read = cache["pages read into cache"];
print(JSON.stringify({
	connections: s.connections.current - 1,
	max_connections: s.connections.current + s.connections.available,
	cache_hits: cache["pages requested from the cache"] - read,
	cache_misses: read,
	commits: ["insert", "query", "update", "delete", "getmore", "command"].reduce((n, op) => n + Number(ops[op]), 0)
}))`

// mongoSlowQueries reads the top command: each collection's totals for
// each kind of operation since the server started, which hold no values.
// Echowa's own commands, and the server's, are in admin, config and local.
var mongoSlowQueries = fmt.Sprintf(`const kinds = {queries: "find", getmore: "getMore", insert: "insert", update: "update", remove: "delete", commands: "command"};
const queries = [];
for (const [ns, totals] of Object.entries(db.adminCommand({top: 1}).totals)) {
	if (!ns.includes(".") || /^(admin|config|local)\./.test(ns)) continue;
	for (const [key, kind] of Object.entries(kinds)) {
		const calls = Number(totals[key]?.count ?? 0), ms = Number(totals[key]?.time ?? 0) / 1000;
		if (calls > 0) queries.push({query: kind + " " + ns, calls, total_ms: ms, mean_ms: ms / calls});
	}
}
queries.sort((a, b) => b.total_ms - a.total_ms);
print(JSON.stringify({since: null, dropped: null, queries: queries.slice(0, %d)}))`, SlowQueryLimit)

// mongoInspect has no reset: the top command's totals last until the
// server restarts.
var mongoInspect = &Inspect{
	// The size is the volume's: the data files and the journal.
	Health:           []string{"sh", "-c", `du -sk /data/db | cut -f1; exec mongosh --quiet --norc --eval "$1"`, "sh", mongoAuth + mongoHealth},
	ParseHealth:      parseSizedHealthJSON,
	SlowQueries:      mongosh(mongoSlowQueries),
	ParseSlowQueries: parseSlowQueryJSON,
}
