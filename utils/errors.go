package utils

type HTTPError struct {
	Status  int
	Message string
}

func (err *HTTPError) Error() string {
	return err.Message
}

type ErrorResponse struct {
	Error   string      `json:"error"`
	Payload interface{} `json:"payload"`
}

//func JsonError(c *gin.Context, e error) {
//	if httpError, ok := e.(*HTTPError); ok {
//		body := gin.H{
//			"status": httpError.Status,
//			"value": map[string]interface{}{
//				"payload": httpError.Response,
//				"message": httpError.Message,
//			},
//		}
//		c.JSON(httpError.Status, body)
//	} else {
//		log.Printf("[INTERNAL SERVER ERROR] [500] [%s]", e)
//		body := gin.H{
//			"status": http.StatusInternalServerError,
//			"value": map[string]interface{}{
//				"message": "Internal server error happend. Details saved in logs",
//				"payload": nil,
//			},
//		}
//		c.JSON(http.StatusInternalServerError, body)
//	}
//}
