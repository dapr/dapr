const express = require('express');
const bodyParser = require('body-parser');

const app = express();
app.use(bodyParser.json());

const port = 3000;

app.post('/A', (req, res) => {
    console.log("A: ", req.body);
    res.sendStatus(200);
});

app.post('/B', (req, res) => {
    console.log("B: ", req.body);
    res.sendStatus(200);
});

app.get('/actions/subscribe', (req, res) => {
    res.json([
        'A',
        'B'
    ]);
})

app.listen(port, () => console.log(`Node App listening on port ${port}!`));