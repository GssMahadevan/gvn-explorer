package db

// Dialect captures the small set of things that differ between SQL engines.
// Application SQL is written once with `?` placeholders and plain INSERTs;
// Rebind and IDReturning adapt it per engine at the single choke point (DB).

type Dialect struct {
	// Name selects the engine's migration directory (migrations/<name>/).
	Name string
	// Rebind rewrites `?` placeholders to the engine's style (identity for
	// engines that natively accept `?`).
	Rebind func(query string) string
	// IDReturning names the id column for "INSERT ... RETURNING <col>";
	// "" means use the driver's LastInsertId instead (sqlite).
	IDReturning string
}

var sqliteDialect = Dialect{
	Name:        "sqlite",
	Rebind:      func(q string) string { return q }, // modernc driver speaks `?`
	IDReturning: "",
}

var postgresDialect = Dialect{
	Name:        "postgres",
	Rebind:      rebindDollar,
	IDReturning: "id",
}

// rebindDollar converts `?` placeholders to $1, $2, ... while leaving
// question marks inside string/identifier literals alone. Backslash escapes
// and doubled quotes skip correctly.
func rebindDollar(q string) string {
	var out []byte
	n := 0
	for i := 0; i < len(q); {
		c := q[i]
		switch c {
		case '\'', '"':
			// copy the literal verbatim, honouring \ escapes and doubled quotes
			quote := c
			out = append(out, c)
			i++
			for i < len(q) {
				if q[i] == '\\' && quote == '\'' && i+1 < len(q) {
					out = append(out, q[i], q[i+1])
					i += 2
					continue
				}
				out = append(out, q[i])
				if q[i] == quote {
					if i+1 < len(q) && q[i+1] == quote {
						out = append(out, quote)
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '?':
			n++
			out = append(out, '$')
			for _, d := range itoa(n) {
				out = append(out, d)
			}
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return string(out)
}

// itoa renders a positive int as digits (avoids importing strconv here just
// for placeholder numbering — strconv is of course fine to use elsewhere).
func itoa(n int) []byte {
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return buf[i:]
}
