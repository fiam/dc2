package api

type Response any

// Tag represents a key-value tag pair
type Tag struct {
	Key   string `url:"Key" xml:"key"`
	Value string `url:"Value" xml:"value"`
}

type CreateTagsResponse struct {
}

type DeleteTagsResponse struct {
}
