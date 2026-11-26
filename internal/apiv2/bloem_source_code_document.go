package apiv2

import "github.com/danielgtaylor/huma/v2"

// BloemSourceCodeResponse describes the source archive of the running version.
type BloemSourceCodeResponse struct {
	License     string `json:"license" doc:"SPDX identifier of the license the source is offered under: AGPL-3.0."`
	Revision    string `json:"revision" doc:"Full commit revision of the running server build; the archive holds exactly this commit."`
	ArchiveName string `json:"archive_name" doc:"File name of the archive, bloem-server-<revision>.tar.gz."`
	ArchivePath string `json:"archive_path" doc:"Path that downloads the archive: /api/bloem/v2/source-code/archive."`
	SizeBytes   int64  `json:"size_bytes" doc:"Size of the archive in bytes."`
}

type BloemSourceCodeOutput struct {
	CacheControl string `header:"Cache-Control" doc:"private, no-store"`
	Body         BloemSourceCodeResponse
}

func registerBloemSourceCodeDocument(reg *Registry) {
	unavailable := "source_unavailable: the archive of the running version is missing. The routes are absent, and source_code_v1 is not advertised, when the server has no archive directory or no clean build revision."

	op := bloemChiDocumentOp(reg, "GET", "/source-code", "getBloemSourceCode", "system", "Describe the complete source code of the running server version.", "bearerAuth", 200)
	op.Description = "Requires any signed-in account; no profile, organization or admin role. Bloem Server is offered under the GNU Affero General Public License v3, and this route names the archive of the exact build that answers it. Availability is advertised by source_code_v1 in /api/bloem/v2/capabilities feature_tokens."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{503: unavailable})
	delete(op.Responses, "403") // any signed-in account may read it
	registerBloemChiDocument[struct{}, BloemSourceCodeOutput](reg, op)

	archive := bloemChiDocumentOp(reg, "GET", "/source-code/archive", "getBloemSourceCodeArchive", "system", "Download the complete source code of the running server version.", "bearerAuth", 200)
	archive.Description = "Requires any signed-in account. A browser download that cannot send an Authorization header may pass the access token as the token query parameter, as media URLs do. The body is a gzip-compressed tar archive of the running build's commit; range requests are supported so a broken download can resume."
	bloemDocumentErrors[BloemNativeError](reg, &archive, map[int]string{503: unavailable})
	delete(archive.Responses, "403")
	archive.Responses["200"] = &huma.Response{
		Description: "The gzip-compressed tar archive of the running build's commit.",
		Headers: map[string]*huma.Header{
			"Content-Type":        {Schema: &huma.Schema{Type: "string", Enum: []any{"application/gzip"}}},
			"Content-Disposition": {Schema: &huma.Schema{Type: "string"}, Description: "attachment; filename=bloem-server-<revision>.tar.gz"},
			"Cache-Control":       {Schema: &huma.Schema{Type: "string"}, Description: "private, no-store"},
			"Accept-Ranges":       {Schema: &huma.Schema{Type: "string"}, Description: "bytes"},
		},
		Content: map[string]*huma.MediaType{"application/gzip": {Schema: &huma.Schema{Type: "string", Format: "binary"}}},
	}
	archive.Responses["206"] = &huma.Response{
		Description: "The requested byte range of the archive.",
		Content:     map[string]*huma.MediaType{"application/gzip": {Schema: &huma.Schema{Type: "string", Format: "binary"}}},
	}
	registerBloemChiDocument[struct{}, struct{}](reg, archive)
}
