.PHONY: dev server web

# Runs the Go server (:8080) and the Vite dev server (:5173) together.
# Open http://localhost:5173 — Vite proxies /api and /ws to Go.
dev:
	@trap 'kill 0' EXIT; \
	(cd server && go run .) & \
	(cd web && npm run dev) & \
	wait

server:
	cd server && go run .

web:
	cd web && npm run dev
