package rex

// Windows has no IANA zoneinfo database, and packaged Rex has no Go installation.
import _ "time/tzdata"
