export const FileSystemUploadType = {
  BINARY_CONTENT: 0,
  MULTIPART: 1,
};

export const createUploadTask = jest.fn((url, fileUri, options, callback) => {
  return {
    uploadAsync: jest.fn().mockResolvedValue({
      status: 200,
      body: "",
      headers: {},
    }),
  };
});
