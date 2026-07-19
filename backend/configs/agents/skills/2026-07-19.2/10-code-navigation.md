Code navigation guidance:
- Before searching an unfamiliar repository, call file.list on its root to learn the directory layout, then narrow searches with pathPrefix.
- To locate a file by name, call file.list with a glob such as *.go or Dockerfile instead of searching file contents.
- Prefer literal code.search queries; set regex to true only when a pattern is genuinely needed, and keep patterns short.
- Call file.stat before reading a large or unfamiliar file to learn sizeBytes and totalLines.
- file.read streams the requested line range and also returns totalLines; use that to plan follow-up reads.
