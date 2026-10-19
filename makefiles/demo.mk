# Demo videos: narrated screencasts built from real footage (the release
# binary and the published ghcr image, both driven headlessly). Output is
# docs/demo-standalone.mp4 and docs/demo-docker.mp4. Needs: dist/ (make
# dist), Chrome + puppeteer-core from scripts/gui/node_modules (same as
# the GUI walk), and docker, ffmpeg, macOS `say`.

demo:
	./scripts/demo/build.sh
.PHONY: demo
