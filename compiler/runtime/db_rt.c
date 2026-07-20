// Copyright 2026 Mark Oxley
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// db_rt.c provides C wrappers around the SQLite C API so that the
// Skink std/db module can open connections, execute statements, and
// read results without needing to deal with pointer-to-pointer types.
//
// Compile and link this file alongside any Skink program that imports std/db.

#include <sqlite3.h>
#include <stddef.h>
#include <stdint.h>

// Open a SQLite database.  Returns an opaque handle (cast sqlite3*) or 0 on
// error.
int64_t Skink_db_open(const char *filename) {
  sqlite3 *db;
  if (sqlite3_open(filename, &db) != SQLITE_OK) {
    if (db) {
      sqlite3_close(db);
    }
    return 0;
  }
  return (int64_t)db;
}

// Close a database connection.  Returns 0 on success, -1 on error.
int Skink_db_close(int64_t handle) {
  if (!handle) {
    return -1;
  }
  int rc = sqlite3_close((sqlite3 *)handle);
  return rc == SQLITE_OK ? 0 : -1;
}

// Return the last error message for a connection.
const char *Skink_db_errmsg(int64_t handle) {
  if (!handle) {
    return "null database handle";
  }
  return sqlite3_errmsg((sqlite3 *)handle);
}

// Execute a non-query SQL statement.  Returns number of rows affected,
// or -1 on error.
int64_t Skink_db_exec(int64_t handle, const char *sql) {
  if (!handle) {
    return -1;
  }
  char *err = NULL;
  int rc = sqlite3_exec((sqlite3 *)handle, sql, NULL, NULL, &err);
  if (rc != SQLITE_OK) {
    if (err) {
      sqlite3_free(err);
    }
    return -1;
  }
  return sqlite3_changes((sqlite3 *)handle);
}

// Prepare a SQL statement.  Returns an opaque statement handle or 0 on error.
int64_t Skink_db_prepare(int64_t handle, const char *sql) {
  if (!handle) {
    return 0;
  }
  sqlite3_stmt *stmt;
  if (sqlite3_prepare_v2((sqlite3 *)handle, sql, -1, &stmt, NULL) !=
      SQLITE_OK) {
    return 0;
  }
  return (int64_t)stmt;
}

// Step a prepared statement.  Returns SQLITE_ROW (100) while rows remain,
// SQLITE_DONE (101) when finished, or an error code otherwise.
int Skink_db_step(int64_t stmt) {
  if (!stmt) {
    return SQLITE_DONE;
  }
  return sqlite3_step((sqlite3_stmt *)stmt);
}

// Return the number of columns in the result set of a prepared statement.
int Skink_db_column_count(int64_t stmt) {
  if (!stmt) {
    return 0;
  }
  return sqlite3_column_count((sqlite3_stmt *)stmt);
}

// Return the text value of a column in the current row.
const char *Skink_db_column_text(int64_t stmt, int col) {
  if (!stmt) {
    return "";
  }
  const unsigned char *txt = sqlite3_column_text((sqlite3_stmt *)stmt, col);
  if (!txt) {
    return "";
  }
  return (const char *)txt;
}

// Return the name of a column in the result set.
const char *Skink_db_column_name(int64_t stmt, int col) {
  if (!stmt) {
    return "";
  }
  return sqlite3_column_name((sqlite3_stmt *)stmt, col);
}

// Finalize a prepared statement, releasing its resources.
void Skink_db_finalize(int64_t stmt) {
  if (stmt) {
    sqlite3_finalize((sqlite3_stmt *)stmt);
  }
}
